package monitor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/impiperl/Tweedle/internal/config"
)

func TestEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
		case "/fail":
			w.WriteHeader(http.StatusBadGateway)
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		}
	}))
	defer srv.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	tls := httptest.NewTLSServer(http.NotFoundHandler())
	defer tls.Close()
	no := false

	cases := []struct {
		ep    config.Endpoint
		level Level
		code  string
		err   string
	}{
		{config.Endpoint{Name: "ok", URL: srv.URL + "/ok"}, LevelOK, "", ""},
		{config.Endpoint{Name: "fail", URL: srv.URL + "/fail"}, LevelError, "endpoint_down", "HTTP 502"},
		{config.Endpoint{Name: "status", URL: srv.URL + "/ok", Status: 204}, LevelError, "endpoint_down", "HTTP 200, expected 204"},
		{config.Endpoint{Name: "timeout", URL: srv.URL + "/slow", Timeout: 100 * time.Millisecond}, LevelError, "endpoint_down", "timeout"},
		{config.Endpoint{Name: "slow", URL: srv.URL + "/slow", Slow: 100 * time.Millisecond}, LevelWarn, "endpoint_slow", ""},
		{config.Endpoint{Name: "closed", URL: closed.URL}, LevelError, "endpoint_down", "refused"},
		{config.Endpoint{Name: "badcert", URL: tls.URL}, LevelError, "endpoint_down", "certificate"},
		{config.Endpoint{Name: "lax", URL: tls.URL, Verify: &no, Status: 404}, LevelOK, "", ""},
	}
	for _, tc := range cases {
		tc.ep.Enabled = true
		if tc.ep.Timeout == 0 {
			tc.ep.Timeout = 2 * time.Second
		}
		st, samples := newEndpointChecker([]config.Endpoint{tc.ep}).check()
		s := samples[0]
		if s.Level != tc.level || s.Code != tc.code || !strings.Contains(st[0].Error, tc.err) {
			t.Errorf("%s: got %s/%s %q, want %s/%s containing %q", tc.ep.Name, s.Level, s.Code, st[0].Error, tc.level, tc.code, tc.err)
		}
		if strings.Contains(st[0].Error, "Get \"") {
			t.Errorf("%s: error not shortened: %q", tc.ep.Name, st[0].Error)
		}
	}
}

func TestEndpointDisabled(t *testing.T) {
	c := newEndpointChecker([]config.Endpoint{{Name: "off", URL: "http://127.0.0.1:1/"}})
	if st, samples := c.check(); len(st) != 0 || len(samples) != 0 {
		t.Fatalf("disabled endpoint was checked: %v", st)
	}
}

func TestDefaultHasNoEndpoints(t *testing.T) {
	if d := config.Default(); len(d.Endpoints) != 0 {
		t.Fatalf("default should watch no other server: %+v", d.Endpoints)
	}
}
