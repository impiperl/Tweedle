package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/impiperl/Tweedle/internal/collect"
	"github.com/impiperl/Tweedle/internal/config"
	"github.com/impiperl/Tweedle/internal/monitor"
	"github.com/impiperl/Tweedle/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store, *httptest.Server) {
	t.Helper()
	cfg := config.Default()
	cfg.Name = "test"
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New(cfg, st, "test")
	m := monitor.New(cfg, srv)
	srv.Attach(m)
	m.Tick()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, st, ts
}

func pair(t *testing.T, st *store.Store, ts *httptest.Server, lang string) string {
	t.Helper()
	code, _, err := NewPairCode(st)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": FormatCode(code), "name": "phone", "lang": lang})
	resp, err := http.Post(ts.URL+"/api/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("pair: %d", resp.StatusCode)
	}
	var out struct{ Token string }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Token
}

func TestPairOnlyOnce(t *testing.T) {
	_, st, ts := newTestServer(t)
	code, _, _ := NewPairCode(st)
	for i, want := range []int{200, 403} {
		resp, err := http.Post(ts.URL+"/api/pair", "application/json", strings.NewReader(`{"code":"`+code+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("attempt %d: got %d, want %d", i, resp.StatusCode, want)
		}
	}
}

func TestLiveSendsStatusAndEvents(t *testing.T) {
	srv, st, ts := newTestServer(t)
	tok := pair(t, st, ts, "de")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/live"
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	read := func() map[string]any {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		return m
	}
	if m := read(); m["type"] != "status" {
		t.Fatalf("first message should be status, got %v", m["type"])
	}
	// The client is registered once the first message was sent.
	srv.Notify(monitor.Event{Time: time.Now(), Key: "proc:x", Level: monitor.LevelError, Code: "proc_down",
		Params: map[string]string{"what": "x", "count": "0", "min": "1"}})
	m := read()
	ev, _ := m["event"].(map[string]any)
	if m["type"] != "event" || ev["message"] != "Prozess x läuft nicht (0 von mindestens 1)" {
		t.Fatalf("unexpected event message: %v", m)
	}
}

// liveConn opens /api/live and reads the first (status) message.
func liveConn(t *testing.T, ctx context.Context, ts *httptest.Server, tok string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/live"
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatal(err)
	}
	return conn
}

// waitClosed reads until the server closes the connection.
func waitClosed(t *testing.T, ctx context.Context, conn *websocket.Conn) {
	t.Helper()
	for {
		_, _, err := conn.Read(ctx)
		if err == nil {
			continue
		}
		if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
			t.Fatalf("expected close by server, got %v", err)
		}
		return
	}
}

func TestLiveClosedWhenDeviceUnpairs(t *testing.T) {
	_, st, ts := newTestServer(t)
	tok := pair(t, st, ts, "en")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := liveConn(t, ctx, ts, tok)
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/device", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	waitClosed(t, ctx, conn)
}

func TestLiveClosedWhenDeviceRemovedElsewhere(t *testing.T) {
	srv, st, ts := newTestServer(t)
	srv.livePing = 50 * time.Millisecond
	tok := pair(t, st, ts, "en")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := liveConn(t, ctx, ts, tok)
	devs, _ := st.Devices()
	if _, err := st.DeleteDevice(devs[0].ID); err != nil { // like "tweedle devices rm"
		t.Fatal(err)
	}
	waitClosed(t, ctx, conn)
}

func TestLiveRequiresToken(t *testing.T) {
	_, _, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/live")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func snapAt(t time.Time, cpu float64) *monitor.Snapshot {
	return &monitor.Snapshot{System: &collect.System{Time: t, CPUPct: cpu,
		Disks: []collect.DiskStat{{Mount: "/", UsedPct: 50}}}}
}

func TestAggregatorAveragesPerMinute(t *testing.T) {
	var a aggregator
	base := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	for i, cpu := range []float64{10, 20, 30} {
		if _, ok := a.add(snapAt(base.Add(time.Duration(i*15)*time.Second), cpu)); ok {
			t.Fatal("minute not finished yet")
		}
	}
	m, ok := a.add(snapAt(base.Add(time.Minute), 99))
	if !ok || m.CPU != 20 || !m.Time.Equal(base) || m.Disks["/"] != 50 {
		t.Fatalf("got %+v ok=%v", m, ok)
	}
}

func TestDownsample(t *testing.T) {
	pts := make([]store.Metric, 720) // → buckets of 2
	for i := range pts {
		pts[i] = store.Metric{CPU: float64(i % 2 * 100)}
	}
	out := downsample(pts, 360)
	if len(out) > 360 || len(out) < 300 {
		t.Fatalf("got %d points", len(out))
	}
	if out[0].CPU != 50 {
		t.Fatalf("expected averaged value 50, got %v", out[0].CPU)
	}
}
