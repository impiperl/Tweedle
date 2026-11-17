package install

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/impiperl/Tweedle/internal/config"
)

// podman makes the test installer behave as if podman were installed, with
// one network "mynet".
var podman bool

func run(t *testing.T, cfg *config.Config, exists bool, answers ...string) (mode, *config.Config, string) {
	t.Helper()
	var out bytes.Buffer
	s := &installer{
		in: bufio.NewReader(strings.NewReader(strings.Join(answers, "\n") + "\n")), out: &out, lang: "en",
		choose: func(*config.Config) (*config.Config, bool, error) {
			t.Fatal("selection must not open when answered no")
			return nil, false, nil
		},
	}
	if podman {
		s.gateway = func(n string) (string, error) {
			if n == "mynet" {
				return "10.89.0.1", nil
			}
			return "", fmt.Errorf("network %s not found", n)
		}
	}
	m, got, err := s.configure(cfg, exists)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("invalid config: %v\n%s", err, out.String())
	}
	return m, got, out.String()
}

func TestProxyMode(t *testing.T) {
	m, cfg, _ := run(t, config.Default(), false,
		"web1", "1", "", "https://example.com/tweedle/", "", "n", "n")
	if m != modeProxy || cfg.Name != "web1" || cfg.Listen != "127.0.0.1:9300" || cfg.PublicURL != "https://example.com/tweedle" {
		t.Fatalf("got %v %+v", m, cfg)
	}
	if len(cfg.Certs.Targets) != 0 {
		t.Fatalf("certificate watch was declined: %+v", cfg.Certs.Targets)
	}
}

func TestProxyModeKeepsPodmanAndWatchesCert(t *testing.T) {
	c := config.Default()
	c.Listen, c.PublicURL = "podman:mynet:9300", "https://example.com/mon"
	podman = true
	defer func() { podman = false }()
	// "nonet" is rejected, then Enter keeps the current network.
	_, cfg, out := run(t, c, true, "", "", "9400", "nonet", "", "", "", "", "n")
	if !strings.Contains(out, "Network not found") || !strings.Contains(out, "on 10.89.0.1") {
		t.Fatalf("output:\n%s", out)
	}
	if cfg.Listen != "podman:mynet:9400" || cfg.PublicURL != "https://example.com/mon" {
		t.Fatalf("got %+v", cfg)
	}
	if len(cfg.Certs.Targets) != 1 || cfg.Certs.Targets[0].Host != "example.com" {
		t.Fatalf("certificate target: %+v", cfg.Certs.Targets)
	}
	var snip bytes.Buffer
	(&installer{out: &snip, lang: "en"}).printProxy(cfg)
	for _, want := range []string{"location /mon/ {", "proxy_pass http://host.containers.internal:9400/;",
		"ProxyPass        /mon/ http://host.containers.internal:9400/ upgrade=websocket", "handle_path /mon/* {",
		"curl https://example.com/mon/health"} {
		if !strings.Contains(snip.String(), want) {
			t.Errorf("missing %q in\n%s", want, snip.String())
		}
	}
}

func TestTLSMode(t *testing.T) {
	cert, key := selfSigned(t, t.TempDir())
	m, cfg, _ := run(t, config.Default(), false,
		"", "2", "example.com", "8443", "/nonexistent.pem", "/nonexistent.key", cert, key, "", "n", "n")
	if m != modeTLS || cfg.Listen != ":8443" || cfg.PublicURL != "https://example.com:8443" ||
		cfg.TLSCert != cert || cfg.TLSKey != key {
		t.Fatalf("got %+v", cfg)
	}
}

func TestPlainMode(t *testing.T) {
	m, cfg, out := run(t, config.Default(), false, "", "3", "abc", "9300", "192.168.1.5", "", "n")
	if m != modePlain || cfg.Listen != ":9300" || cfg.PublicURL != "http://192.168.1.5:9300" {
		t.Fatalf("got %+v", cfg)
	}
	if !strings.Contains(out, "1 to 65535") || !strings.Contains(out, "without encryption") {
		t.Fatalf("expected port hint and warning:\n%s", out)
	}
}

func TestOtherServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tweedle/health" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"name":"other","ok":true,"version":"dev"}`))
	}))
	defer srv.Close()
	_, cfg, out := run(t, config.Default(), false,
		"", "3", "", "10.0.0.1", srv.URL+"/tweedle/", "http://127.0.0.1:1", "n", "", "n")
	if len(cfg.Endpoints) != 1 {
		t.Fatalf("endpoints: %+v\n%s", cfg.Endpoints, out)
	}
	e := cfg.Endpoints[0]
	if e.Name != "other" || e.URL != srv.URL+"/tweedle/health" || !e.Enabled {
		t.Fatalf("endpoint: %+v", e)
	}
	if !strings.Contains(out, "Reachable: other") || !strings.Contains(out, "Not reachable") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestAddEndpointUniqueAndReenable(t *testing.T) {
	c := config.Default()
	c.Endpoints = []config.Endpoint{{Name: "a", URL: "https://a/health"}}
	addEndpoint(c, "a", "https://a/health")
	addEndpoint(c, "a", "https://b/health")
	if len(c.Endpoints) != 2 || !c.Endpoints[0].Enabled || c.Endpoints[1].Name != "a 2" {
		t.Fatalf("got %+v", c.Endpoints)
	}
}

func selfSigned(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.com"},
		DNSNames: []string{"example.com"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return certFile, keyFile
}

func TestProxyModeNewPodmanNetwork(t *testing.T) {
	podman = true
	defer func() { podman = false }()
	_, cfg, _ := run(t, config.Default(), false, "", "1", "", "mynet", "https://example.com/tweedle", "", "n", "n")
	if cfg.Listen != "podman:mynet:9300" {
		t.Fatalf("got %+v", cfg)
	}
}
