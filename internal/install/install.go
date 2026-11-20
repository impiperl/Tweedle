// Package install is "tweedle install": a few questions on the terminal,
// then config, program, systemd service and a health check. Running it again
// changes the settings; the current values are offered as defaults.
package install

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/impiperl/Tweedle/internal/collect"
	"github.com/impiperl/Tweedle/internal/config"
	"github.com/impiperl/Tweedle/internal/i18n"
	"github.com/impiperl/Tweedle/internal/setup"
)

const (
	BinPath  = "/usr/local/bin/tweedle"
	UnitPath = "/etc/systemd/system/tweedle.service"
)

type mode int

const (
	modeProxy mode = iota + 1 // behind a web server that already runs
	modeTLS                   // direct, HTTPS with own certificate
	modePlain                 // direct, no encryption
)

type installer struct {
	in   *bufio.Reader
	out  io.Writer
	lang string
	// choose is the process and container selection (setup.Run; replaced in tests).
	choose func(*config.Config) (*config.Config, bool, error)
	// gateway looks up a podman network's gateway; nil when podman is not installed.
	gateway func(network string) (string, error)
}

func (s *installer) t(code string, p map[string]string) string { return i18n.T(s.lang, code, p) }

func (s *installer) say(code string, p map[string]string) { fmt.Fprintln(s.out, s.t(code, p)) }

// ask shows the question with def in brackets; an empty answer takes def.
func (s *installer) ask(code string, def string, p map[string]string) string {
	q := s.t(code, p)
	if def != "" {
		q += " [" + def + "]"
	}
	fmt.Fprint(s.out, q+": ")
	line, err := s.in.ReadString('\n')
	line = strings.TrimSpace(line)
	if err != nil && line == "" {
		fmt.Fprintln(s.out)
		return def
	}
	if line == "" {
		return def
	}
	return line
}

func (s *installer) yes(code string, def bool, p map[string]string) bool {
	d := s.t("inst_yes_no_n", nil)
	if def {
		d = s.t("inst_yes_no_y", nil)
	}
	a := strings.ToLower(s.ask(code, d, p))
	if a == strings.ToLower(d) {
		return def
	}
	return strings.HasPrefix(a, "j") || strings.HasPrefix(a, "y")
}

func (s *installer) port(def string) string {
	for {
		p := s.ask("inst_port", def, nil)
		if n, err := strconv.Atoi(p); err == nil && n > 0 && n < 65536 {
			return p
		}
		s.say("inst_port_invalid", nil)
	}
}

// Run asks, writes the config at path and installs the service.
func Run(path string, in io.Reader, out io.Writer) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("%s", i18n.T(langFromEnv(), "inst_need_root", nil))
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return fmt.Errorf("%s", i18n.T(langFromEnv(), "inst_need_systemd", nil))
	}
	cfg, err := config.Load(path)
	exists := err == nil
	if os.IsNotExist(err) {
		cfg = config.Default()
		cfg.Lang = langFromEnv()
	} else if err != nil {
		return err
	}
	s := &installer{in: bufio.NewReader(in), out: out, lang: cfg.Lang, choose: setup.Run}
	if _, err := exec.LookPath(cfg.PodmanCmd[len(cfg.PodmanCmd)-1]); err == nil {
		s.gateway = func(n string) (string, error) { return collect.PodmanGateway(cfg.PodmanCmd, n) }
	}
	s.say("inst_intro", map[string]string{"path": path})
	m, cfg, err := s.configure(cfg, exists)
	if err != nil {
		return err
	}

	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := cfg.Save(path); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	s.say("inst_saved", map[string]string{"path": path})

	if err := installBinary(); err != nil {
		return err
	}
	if err := installService(path); err != nil {
		return err
	}
	s.say("inst_service_ok", nil)

	s.healthCheck(cfg)
	if m == modeProxy {
		s.printProxy(cfg)
	} else {
		s.say("inst_firewall", map[string]string{"port": portOf(cfg.Listen)})
	}
	s.say("inst_done", map[string]string{"url": cfg.PublicURL})
	return nil
}

// configure asks the questions and returns the chosen mode and the new config.
func (s *installer) configure(cfg *config.Config, exists bool) (mode, *config.Config, error) {
	cfg.Name = s.ask("inst_name", cfg.Name, nil)

	fmt.Fprintln(s.out)
	s.say("inst_mode_q", nil)
	for i, code := range []string{"inst_mode_proxy", "inst_mode_tls", "inst_mode_plain"} {
		fmt.Fprintf(s.out, "  %d) %s\n", i+1, s.t(code, nil))
	}
	prev := currentMode(cfg)
	m := prev
	for {
		a := s.ask("inst_choice", strconv.Itoa(int(m)), nil)
		if n, err := strconv.Atoi(a); err == nil && n >= 1 && n <= 3 {
			m = mode(n)
			break
		}
	}
	curPort := portOf(cfg.Listen)
	if curPort == "" {
		curPort = "9300"
	}

	switch m {
	case modeProxy:
		port := s.port(curPort)
		curNet, _, _, _ := cfg.ListenPodman()
		cfg.Listen = "127.0.0.1:" + port
		if network := s.podmanNetwork(curNet); network != "" {
			cfg.Listen = "podman:" + network + ":" + port // web server in a container
		}
		cfg.TLSCert, cfg.TLSKey = "", ""
		def := cfg.PublicURL
		if def == "" || prev != modeProxy {
			def = "https://" + hostname() + "/tweedle"
		}
		cfg.PublicURL = s.askURL("inst_public_url", def)
	case modeTLS:
		domain := s.ask("inst_domain", publicHost(cfg), nil)
		port := s.port(curPort)
		for {
			defCert, defKey := cfg.TLSCert, cfg.TLSKey
			if defCert == "" {
				defCert, defKey = letsEncrypt(domain)
			}
			cert := s.ask("inst_cert", defCert, nil)
			key := s.ask("inst_key", defKey, nil)
			if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
				s.say("inst_cert_invalid", map[string]string{"error": err.Error()})
				continue
			}
			cfg.TLSCert, cfg.TLSKey = cert, key
			break
		}
		cfg.Listen = ":" + port
		cfg.PublicURL = "https://" + hostPort(domain, port, "443")
	case modePlain:
		port := s.port(curPort)
		addr := s.ask("inst_address", plainHost(cfg), nil)
		cfg.TLSCert, cfg.TLSKey = "", ""
		cfg.Listen = ":" + port
		cfg.PublicURL = "http://" + hostPort(addr, port, "80")
		s.say("inst_plain_warning", nil)
	}

	s.otherServers(cfg)
	s.certTarget(cfg)

	fmt.Fprintln(s.out)
	if s.yes("inst_select_q", !exists || len(cfg.Processes)+len(cfg.Containers) == 0, nil) {
		sel, ok, err := s.choose(cfg)
		if err != nil {
			return 0, nil, err
		}
		if ok {
			cfg = sel
		}
	}
	if len(cfg.Containers) > 0 {
		fmt.Fprintln(s.out)
		s.say("inst_logs_warning", map[string]string{"path": config.DefaultPath})
		cfg.Logs.Enabled = s.yes("inst_logs_q", cfg.Logs.Enabled, nil)
	}
	return m, cfg, nil
}

// podmanNetwork asks whether the web server runs in a podman container and
// returns its network ("" = no). Only asked when podman is installed.
func (s *installer) podmanNetwork(cur string) string {
	if s.gateway == nil {
		return ""
	}
	for {
		n := s.ask("inst_podman_q", cur, nil)
		if n == "" || n == "-" {
			return ""
		}
		gw, err := s.gateway(n)
		if err == nil {
			s.say("inst_podman_ok", map[string]string{"network": n, "gateway": gw})
			return n
		}
		s.say("inst_podman_failed", map[string]string{"error": err.Error()})
	}
}

func (s *installer) askURL(code, def string) string {
	for {
		v := strings.TrimRight(s.ask(code, def, nil), "/")
		u, err := url.Parse(v)
		if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			return v
		}
		s.say("inst_url_invalid", nil)
	}
}

// otherServers adds other Tweedle servers to watch (their /health).
func (s *installer) otherServers(cfg *config.Config) {
	fmt.Fprintln(s.out)
	for _, e := range cfg.Endpoints {
		state := s.t("inst_on", nil)
		if !e.Enabled {
			state = s.t("inst_off", nil)
		}
		s.say("inst_watching", map[string]string{"name": e.Name, "url": e.URL, "state": state})
	}
	for {
		v := strings.TrimRight(s.ask("inst_other_q", "", nil), "/")
		if v == "" {
			return
		}
		if !strings.Contains(v, "://") {
			v = "https://" + v
		}
		if !strings.HasSuffix(v, "/health") {
			v += "/health"
		}
		u, err := url.Parse(v)
		if err != nil || u.Host == "" {
			s.say("inst_url_invalid", nil)
			continue
		}
		name, err := fetchName(v)
		if err != nil {
			s.say("inst_other_unreachable", map[string]string{"error": err.Error()})
			if !s.yes("inst_other_anyway", true, nil) {
				continue
			}
			name = u.Hostname()
		} else {
			s.say("inst_other_ok", map[string]string{"name": name})
		}
		addEndpoint(cfg, name, v)
	}
}

// addEndpoint adds url, or switches an existing entry with that url on.
func addEndpoint(cfg *config.Config, name, u string) {
	for i, e := range cfg.Endpoints {
		if e.URL == u {
			cfg.Endpoints[i].Enabled = true
			return
		}
	}
	used := map[string]bool{}
	for _, e := range cfg.Endpoints {
		used[e.Name] = true
	}
	base := name
	for n := 2; used[name]; n++ {
		name = fmt.Sprintf("%s %d", base, n)
	}
	cfg.Endpoints = append(cfg.Endpoints, config.Endpoint{Name: name, URL: u, Enabled: true})
}

// certTarget offers to watch the certificate of the public address.
func (s *installer) certTarget(cfg *config.Config) {
	u, err := url.Parse(cfg.PublicURL)
	if err != nil || u.Scheme != "https" {
		return
	}
	host := hostPort(u.Hostname(), u.Port(), "443")
	for _, t := range cfg.Certs.Targets {
		if t.Host == host || t.Host == u.Hostname() || t.Name == u.Hostname() {
			return
		}
	}
	if s.yes("inst_cert_watch_q", true, map[string]string{"host": u.Hostname()}) {
		cfg.Certs.Targets = append(cfg.Certs.Targets, config.CertTarget{Name: u.Hostname(), Host: host})
	}
}

func fetchName(healthURL string) (string, error) {
	c := &http.Client{Timeout: 8 * time.Second}
	resp, err := c.Get(healthURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var h struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&h); err != nil || h.Name == "" {
		return "", fmt.Errorf("not a Tweedle server")
	}
	return h.Name, nil
}

func (s *installer) healthCheck(cfg *config.Config) {
	host, port, _ := net.SplitHostPort(cfg.Listen)
	if network, p, ok, _ := cfg.ListenPodman(); ok && s.gateway != nil {
		port = p
		host, _ = s.gateway(network)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "http"
	tr := &http.Transport{}
	if cfg.TLSCert != "" {
		scheme = "https"
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // local check only
	}
	local := scheme + "://" + net.JoinHostPort(host, port) + "/health"
	c := &http.Client{Timeout: 3 * time.Second, Transport: tr}
	for i := 0; i < 15; i++ {
		if resp, err := c.Get(local); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				s.say("inst_health_ok", map[string]string{"url": local})
				return
			}
		}
		time.Sleep(time.Second)
	}
	s.say("inst_health_failed", map[string]string{"url": local})
}

func (s *installer) printProxy(cfg *config.Config) {
	u, _ := url.Parse(cfg.PublicURL)
	prefix := strings.TrimRight(u.Path, "/") + "/"
	port := portOf(cfg.Listen)
	target := "127.0.0.1:" + port
	if _, _, ok, _ := cfg.ListenPodman(); ok {
		target = "host.containers.internal:" + port
	}
	p := map[string]string{"prefix": prefix, "target": target, "host": u.Hostname(),
		"match": strings.TrimSuffix(prefix, "/") + "/*", "health": cfg.PublicURL + "/health"}
	fmt.Fprintln(s.out)
	s.say("inst_proxy_intro", p)
	fmt.Fprintln(s.out, "\n# nginx")
	fmt.Fprintln(s.out, expand(nginxSnippet, p))
	fmt.Fprintln(s.out, "# Apache 2.4.47+ (mod_proxy, mod_proxy_http)")
	fmt.Fprintln(s.out, expand(apacheSnippet, p))
	fmt.Fprintln(s.out, "# Caddy")
	fmt.Fprintln(s.out, expand(caddySnippet, p))
	s.say("inst_proxy_check", p)
}

const nginxSnippet = `location {prefix} {
    proxy_pass http://{target}/;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $http_connection;
    proxy_buffering off;
    proxy_read_timeout 120s;
}
`

const apacheSnippet = `ProxyPass        {prefix} http://{target}/ upgrade=websocket
ProxyPassReverse {prefix} http://{target}/
`

const caddySnippet = `handle_path {match} {
    reverse_proxy {target}
}
`

func expand(s string, p map[string]string) string {
	for k, v := range p {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

func installBinary() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	if exe == BinPath {
		return nil
	}
	src, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer src.Close()
	// Write next to the target and rename, so a running service keeps its file.
	tmp := BinPath + ".new"
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, BinPath)
}

const unit = `[Unit]
Description=Tweedle monitoring agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s serve --config %s
Restart=always
RestartSec=10
User=root

[Install]
WantedBy=multi-user.target
`

func installService(configPath string) error {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(UnitPath, []byte(fmt.Sprintf(unit, BinPath, abs)), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "tweedle"}, {"restart", "tweedle"}} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func currentMode(cfg *config.Config) mode {
	if cfg.TLSCert != "" {
		return modeTLS
	}
	if _, _, ok, _ := cfg.ListenPodman(); ok {
		return modeProxy
	}
	host, _, _ := net.SplitHostPort(cfg.Listen)
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return modeProxy
	}
	return modePlain
}

func portOf(listen string) string {
	if _, p, ok, _ := (&config.Config{Listen: listen}).ListenPodman(); ok {
		return p
	}
	_, p, _ := net.SplitHostPort(listen)
	return p
}

func hostPort(host, port, std string) string {
	if port == "" || port == std {
		return host
	}
	return net.JoinHostPort(host, port)
}

func publicHost(cfg *config.Config) string {
	if u, err := url.Parse(cfg.PublicURL); err == nil && u.Hostname() != "" && net.ParseIP(u.Hostname()) == nil {
		return u.Hostname()
	}
	return hostname()
}

func plainHost(cfg *config.Config) string {
	if u, err := url.Parse(cfg.PublicURL); err == nil && u.Scheme == "http" && u.Hostname() != "" {
		return u.Hostname()
	}
	if ip := firstIPv4(); ip != "" {
		return ip
	}
	return hostname()
}

// hostname is the full host name if the system knows one.
func hostname() string {
	h, _ := os.Hostname()
	if out, err := exec.Command("hostname", "-f").Output(); err == nil {
		if f := strings.TrimSpace(string(out)); strings.Contains(f, ".") {
			return f
		}
	}
	return h
}

func firstIPv4() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			return n.IP.String()
		}
	}
	return ""
}

func letsEncrypt(domain string) (cert, key string) {
	dir := filepath.Join("/etc/letsencrypt/live", domain)
	if _, err := os.Stat(filepath.Join(dir, "fullchain.pem")); err == nil {
		return filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")
	}
	return "", ""
}

func langFromEnv() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			if strings.HasPrefix(v, "de") {
				return "de"
			}
			return "en"
		}
	}
	return "en"
}
