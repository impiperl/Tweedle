package monitor

import (
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTestTracker(failAfter int) (*Tracker, *clock) {
	c := &clock{t: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	tr := NewTracker(failAfter)
	tr.now = c.now
	return tr, c
}

func sample(level Level) Sample {
	return Sample{Key: "disk:/", Level: level, Code: "disk_high", Label: "lbl_disk", Params: map[string]string{"mount": "/"}}
}

func feed(tr *Tracker, c *clock, level Level, n int) []Event {
	var out []Event
	for i := 0; i < n; i++ {
		c.add(15 * time.Second)
		out = append(out, tr.Observe(sample(level))...)
	}
	return out
}

func TestAlarmOnlyAfterFailAfter(t *testing.T) {
	tr, c := newTestTracker(3)
	if ev := feed(tr, c, LevelError, 2); len(ev) != 0 {
		t.Fatalf("expected no event after 2 samples, got %v", ev)
	}
	ev := feed(tr, c, LevelError, 1)
	if len(ev) != 1 || ev[0].Level != LevelError || ev[0].Code != "disk_high" {
		t.Fatalf("expected one error event, got %v", ev)
	}
	if ev := feed(tr, c, LevelError, 5); len(ev) != 0 {
		t.Fatalf("expected no repeat while still bad, got %v", ev)
	}
}

func TestSingleGoodSampleResetsCount(t *testing.T) {
	tr, c := newTestTracker(3)
	feed(tr, c, LevelError, 2)
	feed(tr, c, LevelOK, 1)
	if ev := feed(tr, c, LevelError, 2); len(ev) != 0 {
		t.Fatalf("count should have restarted, got %v", ev)
	}
}

func TestRecoveryNeedsFailAfterGoodSamples(t *testing.T) {
	tr, c := newTestTracker(3)
	feed(tr, c, LevelError, 3)
	if ev := feed(tr, c, LevelOK, 2); len(ev) != 0 {
		t.Fatalf("recovery too early: %v", ev)
	}
	ev := feed(tr, c, LevelOK, 1)
	if len(ev) != 1 || ev[0].Level != LevelInfo || ev[0].Code != "recovered" || ev[0].Params["label"] != "lbl_disk" {
		t.Fatalf("expected recovered info, got %v", ev)
	}
}

func TestErrorToWarn(t *testing.T) {
	tr, c := newTestTracker(2)
	feed(tr, c, LevelError, 2)
	ev := feed(tr, c, LevelWarn, 2)
	if len(ev) != 1 || ev[0].Level != LevelWarn {
		t.Fatalf("expected warn event, got %v", ev)
	}
}

func TestNeedDelaysAlarmButNotRecovery(t *testing.T) {
	tr, c := newTestTracker(3)
	var ev []Event
	for i := 0; i < 19; i++ {
		c.add(15 * time.Second)
		s := sample(LevelWarn)
		s.Need = 20
		ev = append(ev, tr.Observe(s)...)
	}
	if len(ev) != 0 {
		t.Fatalf("alarm before Need reached: %v", ev)
	}
	s := sample(LevelWarn)
	s.Need = 20
	if ev := tr.Observe(s); len(ev) != 1 {
		t.Fatalf("expected alarm at Need, got %v", ev)
	}
	if ev := feed(tr, c, LevelOK, 3); len(ev) != 1 || ev[0].Code != "recovered" {
		t.Fatalf("expected recovery after FailAfter, got %v", ev)
	}
}

func TestFlappingMutesAndResumes(t *testing.T) {
	tr, c := newTestTracker(1)
	var ev []Event
	for i := 0; i < 10; i++ {
		lvl := LevelError
		if i%2 == 1 {
			lvl = LevelOK
		}
		ev = append(ev, feed(tr, c, lvl, 1)...)
	}
	// FlapMax=6: five normal events, then one "flapping", then silence.
	if len(ev) != 6 || ev[5].Code != "flapping" {
		t.Fatalf("expected 5 events + flapping, got %d: %v", len(ev), ev)
	}
	// Ends in error state (i=9 is ok → last fed was ok). Feed error once more, then wait.
	feed(tr, c, LevelError, 1)
	c.add(31 * time.Minute)
	ev = feed(tr, c, LevelError, 1)
	if len(ev) != 2 || ev[0].Code != "flapping_end" || ev[1].Level != LevelError || ev[1].Code != "disk_high" {
		t.Fatalf("expected flapping_end + current error, got %v", ev)
	}
}

func TestOneShotCooldown(t *testing.T) {
	tr, c := newTestTracker(3)
	if ev := tr.OneShot("proc:x", LevelWarn, "proc_restarted", nil); len(ev) != 1 {
		t.Fatalf("first one-shot missing")
	}
	c.add(5 * time.Minute)
	if ev := tr.OneShot("proc:x", LevelWarn, "proc_restarted", nil); len(ev) != 0 {
		t.Fatalf("one-shot within cooldown should be muted")
	}
	c.add(6 * time.Minute)
	if ev := tr.OneShot("proc:x", LevelWarn, "proc_restarted", nil); len(ev) != 1 {
		t.Fatalf("one-shot after cooldown missing")
	}
}
