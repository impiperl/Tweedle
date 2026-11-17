// Command tweedle is the monitoring agent: "install" sets it up as a
// service, "setup" chooses what to watch, "serve" runs the checks, "status"
// prints the current state once.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"

	"github.com/impiperl/Tweedle/internal/api"
	"github.com/impiperl/Tweedle/internal/collect"
	"github.com/impiperl/Tweedle/internal/config"
	"github.com/impiperl/Tweedle/internal/i18n"
	"github.com/impiperl/Tweedle/internal/install"
	"github.com/impiperl/Tweedle/internal/monitor"
	"github.com/impiperl/Tweedle/internal/push"
	"github.com/impiperl/Tweedle/internal/setup"
	"github.com/impiperl/Tweedle/internal/store"
)

var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `tweedle %s

  tweedle install [--config PATH]  set up or change: questions, config, systemd service
  tweedle setup  [--config PATH]   choose processes and containers to watch
  tweedle serve  [--config PATH]   run the checks (systemd service)
  tweedle status [--config PATH]   print the current state once
  tweedle pair   [--config PATH] [--url URL]   show a QR code to pair the Dee app
  tweedle devices [--config PATH] [rm ID]      list or remove paired devices
  tweedle testpush [--config PATH]             send a test push to all devices
  tweedle version

Default config: %s
`, version, config.DefaultPath)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath, "config file")
	pairURL := fs.String("url", "", "public URL for pairing (default: public_url from the config)")
	_ = fs.Parse(args)

	var err error
	switch cmd {
	case "install":
		err = install.Run(*cfgPath, os.Stdin, os.Stdout)
	case "setup":
		err = runSetup(*cfgPath)
	case "serve":
		err = runServe(*cfgPath)
	case "status":
		err = runStatus(*cfgPath)
	case "pair":
		err = runPair(*cfgPath, *pairURL)
	case "devices":
		err = runDevices(*cfgPath, fs.Args())
	case "testpush":
		err = runTestPush(*cfgPath)
	case "version", "--version", "-v":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tweedle:", err)
		os.Exit(1)
	}
}

func runSetup(path string) error {
	cfg, err := config.LoadOrDefault(path)
	if err != nil {
		return err
	}
	out, ok, err := setup.Run(cfg)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println(i18n.T(cfg.Lang, "setup_aborted", nil))
		return nil
	}
	if err := out.Save(path); err != nil {
		return err
	}
	fmt.Println(i18n.T(cfg.Lang, "setup_saved", map[string]string{"path": path}))
	fmt.Println(i18n.T(cfg.Lang, "setup_restart", nil))
	return nil
}

// resolveListen turns "podman:NETWORK:PORT" into the gateway address of that
// network. The network has to exist; if its bridge is not up yet, listening
// fails and systemd starts tweedle again.
func resolveListen(cfg *config.Config) error {
	network, port, ok, err := cfg.ListenPodman()
	if !ok || err != nil {
		return err
	}
	gw, err := collect.PodmanGateway(cfg.PodmanCmd, network)
	if err != nil {
		return err
	}
	cfg.Listen = net.JoinHostPort(gw, port)
	return nil
}

func runServe(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if err := resolveListen(cfg); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	srv := api.New(cfg, st, version)
	pusher := push.New(cfg, st)
	srv.Push = pusher
	go pusher.Run(ctx)
	m := monitor.New(cfg, &monitor.LogNotifier{Lang: cfg.Lang, Out: os.Stdout}, srv)
	srv.Attach(m)

	errc := make(chan error, 1)
	go func() { errc <- srv.Run(ctx) }()
	go m.Run(ctx)
	fmt.Printf("tweedle %s: %s, API %s\n", version, cfg.Name, cfg.Listen)
	select {
	case err := <-errc:
		stop()
		return err
	case <-ctx.Done():
		return <-errc
	}
}

func runPair(path, url string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if url == "" {
		url = cfg.PublicURL
	}
	if url == "" {
		return fmt.Errorf("%s", i18n.T(cfg.Lang, "pair_no_url", nil))
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	code, exp, err := api.NewPairCode(st)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"tweedle": 1, "name": cfg.Name, "url": strings.TrimRight(url, "/"), "code": code})
	qrterminal.GenerateWithConfig(string(payload), qrterminal.Config{
		Level: qrterminal.L, Writer: os.Stdout, HalfBlocks: true,
		BlackChar: qrterminal.BLACK_BLACK, WhiteBlackChar: qrterminal.WHITE_BLACK,
		WhiteChar: qrterminal.WHITE_WHITE, BlackWhiteChar: qrterminal.BLACK_WHITE, QuietZone: 2,
	})
	fmt.Println(i18n.T(cfg.Lang, "pair_info", map[string]string{
		"name": cfg.Name, "url": url, "code": api.FormatCode(code), "time": exp.Format("15:04"),
	}))
	return nil
}

func runDevices(path string, args []string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	if len(args) == 2 && args[0] == "rm" {
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid id %q", args[1])
		}
		ok, err := st.DeleteDevice(id)
		if err != nil {
			return err
		}
		code := "devices_removed"
		if !ok {
			code = "devices_not_found"
		}
		fmt.Println(i18n.T(cfg.Lang, code, map[string]string{"id": args[1]}))
		return nil
	}
	if len(args) > 0 {
		usage()
		os.Exit(2)
	}
	devs, err := st.Devices()
	if err != nil {
		return err
	}
	if len(devs) == 0 {
		fmt.Println(i18n.T(cfg.Lang, "devices_none", nil))
		return nil
	}
	for _, d := range devs {
		push := "-"
		if d.PushToken != "" {
			push = "push"
		}
		fmt.Printf("%4d  %-24s %-3s %-5s %-5s %s\n", d.ID, d.Name, d.Lang, d.MinLevel, push, d.LastSeen.Format("2006-01-02 15:04"))
	}
	return nil
}

func runStatus(path string) error {
	cfg, err := config.LoadOrDefault(path)
	if err != nil {
		return err
	}
	// Status only shows the state; a single bad sample is shown as-is.
	cfg.FailAfter = 1
	for _, l := range []*config.Limit{&cfg.Thresholds.CPU, &cfg.Thresholds.Memory, &cfg.Thresholds.Swap,
		&cfg.Thresholds.Disk, &cfg.Thresholds.Inodes, &cfg.Thresholds.LoadPerCore} {
		l.For = 0
	}
	m := monitor.New(cfg)
	m.Tick() // primes per-process CPU
	time.Sleep(time.Second)
	printSnapshot(cfg.Lang, m.Tick())
	return nil
}

func printSnapshot(lang string, s *monitor.Snapshot) {
	t := func(code string) string { return i18n.T(lang, code, nil) }
	lvl := func(key string) string {
		l := s.Levels[key]
		if l == "" {
			l = monitor.LevelOK
		}
		return t("level_" + string(l))
	}
	fmt.Printf("%s\n\n", s.Server)
	if sys := s.System; sys != nil {
		fmt.Printf("  %-7s %-6s %5.1f %%   (%d cores)\n", "CPU", lvl("cpu"), sys.CPUPct, sys.Cores)
		fmt.Printf("  %-7s %-6s %.2f %.2f %.2f\n", "Load", lvl("load"), sys.Load1, sys.Load5, sys.Load15)
		fmt.Printf("  %-7s %-6s %5.1f %%   %s / %s\n", "RAM", lvl("mem"), sys.MemPct,
			monitor.HumanBytes(sys.MemUsed), monitor.HumanBytes(sys.MemTotal))
		if sys.SwapTotal > 0 {
			fmt.Printf("  %-7s %-6s %5.1f %%   %s / %s\n", "Swap", lvl("swap"), sys.SwapPct,
				monitor.HumanBytes(sys.SwapUsed), monitor.HumanBytes(sys.SwapTotal))
		}
		for _, d := range sys.Disks {
			fmt.Printf("  %-7s %-6s %5.1f %%   %s / %s   inodes %.1f %% %s   %s\n", "Disk", lvl("disk:"+d.Mount), d.UsedPct,
				monitor.HumanBytes(d.Used), monitor.HumanBytes(d.Total), d.InodesPct, lvl("inodes:"+d.Mount), d.Mount)
		}
		fmt.Printf("  %-7s        ↓ %s/s  ↑ %s/s\n", "Net", monitor.HumanBytes(uint64(sys.NetRxRate)), monitor.HumanBytes(uint64(sys.NetTxRate)))
		fmt.Printf("  %-7s        %s\n", "Uptime", (time.Duration(sys.Uptime) * time.Second).String())
	}
	fmt.Printf("\n%s\n", t("status_processes"))
	if len(s.Processes) == 0 {
		fmt.Println("  " + t("status_none"))
	}
	for _, p := range s.Processes {
		fmt.Printf("  %-6s %-24s %d/%d   CPU %5.1f %%   RSS %s\n", t("level_"+string(p.Level)), p.Name, p.Count, p.MinCount,
			p.CPUPct, monitor.HumanBytes(p.RSS))
	}
	fmt.Printf("\n%s\n", t("status_containers"))
	if s.PodmanErr != "" {
		fmt.Println("  podman: " + s.PodmanErr)
	} else if len(s.Containers) == 0 {
		fmt.Println("  " + t("status_none"))
	}
	sort.Slice(s.Containers, func(i, j int) bool { return s.Containers[i].Name < s.Containers[j].Name })
	for _, c := range s.Containers {
		fmt.Printf("  %-6s %-24s %s\n", t("level_"+string(c.Level)), c.Name, strings.TrimSpace(c.Status))
	}
	if len(s.Certs) > 0 {
		fmt.Printf("\n%s\n", t("status_certs"))
	}
	for _, c := range s.Certs {
		detail := ""
		if !c.NotAfter.IsZero() {
			detail = c.NotAfter.Format("2006-01-02")
			if c.DaysLeft >= 0 {
				detail += "  " + i18n.T(lang, "status_days", map[string]string{"days": fmt.Sprint(c.DaysLeft)})
			} else {
				detail += "  " + t("status_expired")
			}
		}
		if c.Error != "" {
			detail += "  " + c.Error
		}
		fmt.Printf("  %-6s %-24s %s\n", t("level_"+string(c.Level)), c.Name, strings.TrimSpace(detail))
	}
	if len(s.Endpoints) > 0 {
		fmt.Printf("\n%s\n", t("status_endpoints"))
	}
	for _, e := range s.Endpoints {
		detail := fmt.Sprintf("%d ms", e.LatencyMs)
		if e.Status != 0 {
			detail = fmt.Sprintf("HTTP %d  %s", e.Status, detail)
		}
		if e.Error != "" {
			detail = e.Error
		}
		fmt.Printf("  %-6s %-24s %s\n", t("level_"+string(e.Level)), e.Name, detail)
	}
}

func runTestPush(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sent, failed, skipped, err := push.New(cfg, st).SendTest(ctx)
	if err != nil {
		return err
	}
	fmt.Println(i18n.T(cfg.Lang, "push_test_result", map[string]string{
		"sent": strconv.Itoa(sent), "failed": strconv.Itoa(failed), "skipped": strconv.Itoa(skipped),
	}))
	return nil
}
