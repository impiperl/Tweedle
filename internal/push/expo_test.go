package push

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/impiperl/Tweedle/internal/config"
	"github.com/impiperl/Tweedle/internal/store"
)

type fakeExpo struct {
	mu       sync.Mutex
	received []Message
	// tokens that Expo reports as DeviceNotRegistered
	gone map[string]bool
}

func (f *fakeExpo) handler(w http.ResponseWriter, r *http.Request) {
	var msgs []Message
	_ = json.NewDecoder(r.Body).Decode(&msgs)
	f.mu.Lock()
	f.received = append(f.received, msgs...)
	f.mu.Unlock()
	var data []map[string]any
	for i, m := range msgs {
		if f.gone[m.To] {
			data = append(data, map[string]any{"status": "error", "message": "gone",
				"details": map[string]string{"error": "DeviceNotRegistered"}})
		} else {
			data = append(data, map[string]any{"status": "ok", "id": "ticket-" + string(rune('a'+i))})
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func setup(t *testing.T, devices ...store.Device) (*Expo, *store.Store, *fakeExpo) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for i, d := range devices {
		code := "code" + string(rune('a'+i))
		_ = st.AddPairCode(code, time.Now().Add(time.Minute))
		if _, err := st.Pair(code, "tok"+code, d); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeExpo{gone: map[string]bool{}}
	ts := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(ts.Close)
	cfg := config.Default()
	cfg.Name = "prod"
	cfg.PublicURL = "https://example.org/mon"
	e := New(cfg, st)
	e.SendURL = ts.URL
	return e, st, f
}

func ev(id int64, level, code string, params map[string]string) store.Event {
	return store.Event{ID: id, Time: time.Now(), Key: "k", Level: level, Code: code, Params: params}
}

func TestMinLevelAndLanguage(t *testing.T) {
	e, _, f := setup(t,
		store.Device{Name: "a", Lang: "de", PushToken: "A", MinLevel: "warn"},
		store.Device{Name: "b", Lang: "en", PushToken: "B", MinLevel: "error"},
		store.Device{Name: "c", Lang: "en", PushToken: "", MinLevel: "info"},
		store.Device{Name: "d", Lang: "en", PushToken: "D", MinLevel: "none"},
	)
	e.sendEvents(context.Background(), []store.Event{
		ev(1, "info", "started", map[string]string{"server": "prod"}),
		ev(2, "warn", "proc_restarted", map[string]string{"what": "nginx"}),
		ev(3, "error", "proc_down", map[string]string{"what": "nginx", "count": "0", "min": "1"}),
	})
	got := map[string][]string{}
	for _, m := range f.received {
		got[m.To] = append(got[m.To], m.Body)
	}
	if len(got["A"]) != 2 || got["A"][0] != "Prozess nginx wurde neu gestartet" {
		t.Errorf("A (de, warn): %v", got["A"])
	}
	if len(got["B"]) != 1 || got["B"][0] != "Process nginx is not running (0 of at least 1)" {
		t.Errorf("B (en, error): %v", got["B"])
	}
	if len(got["D"]) != 0 || len(got[""]) != 0 {
		t.Errorf("none/without token must get nothing: %v", got)
	}
	for _, m := range f.received {
		if m.To == "B" && (m.ChannelID != "error" || m.Priority != "high" || m.Title != "prod · ERROR") {
			t.Errorf("error message fields: %+v", m)
		}
	}
}

func TestBurstBecomesSummary(t *testing.T) {
	e, _, f := setup(t, store.Device{Name: "a", Lang: "de", PushToken: "A", MinLevel: "warn"})
	var evs []store.Event
	for i := 0; i < 5; i++ {
		evs = append(evs, ev(int64(i+1), "warn", "container_restarted", map[string]string{"what": "c"}))
	}
	evs = append(evs, ev(6, "error", "container_down", map[string]string{"what": "db", "state": "exited"}))
	e.sendEvents(context.Background(), evs)
	if len(f.received) != 1 {
		t.Fatalf("expected one summary, got %d", len(f.received))
	}
	m := f.received[0]
	if m.ChannelID != "error" || !strings.HasPrefix(m.Body, "6 Meldungen, u. a.: Container db läuft nicht") {
		t.Fatalf("summary: %+v", m)
	}
}

func TestDeviceNotRegisteredClearsToken(t *testing.T) {
	e, st, f := setup(t, store.Device{Name: "a", Lang: "de", PushToken: "A", MinLevel: "warn"})
	f.gone["A"] = true
	e.sendEvents(context.Background(), []store.Event{ev(1, "error", "proc_down", map[string]string{"what": "x"})})
	devs, _ := st.Devices()
	if devs[0].PushToken != "" {
		t.Fatalf("token should be cleared, got %q", devs[0].PushToken)
	}
}

func TestDebounceCollectsBurst(t *testing.T) {
	e, _, f := setup(t, store.Device{Name: "a", Lang: "de", PushToken: "A", MinLevel: "warn"})
	e.cfg.Push.Debounce = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.Run(ctx)
	for i := 0; i < 5; i++ {
		e.Enqueue(ev(int64(i+1), "warn", "proc_restarted", map[string]string{"what": "x"}))
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.received) != 1 || !strings.HasPrefix(f.received[0].Body, "5 Meldungen") {
		t.Fatalf("expected one summary after debounce, got %d: %+v", len(f.received), f.received)
	}
}
