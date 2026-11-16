package monitor

import (
	"time"
)

// Level of a check result or an event.
type Level string

const (
	LevelOK    Level = "ok"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

func (l Level) rank() int {
	switch l {
	case LevelWarn:
		return 2
	case LevelError:
		return 3
	case LevelInfo:
		return 1
	}
	return 0
}

// Event is something worth telling: a state change or a one-off happening.
type Event struct {
	Time   time.Time         `json:"time"`
	Server string            `json:"server"`
	Key    string            `json:"key"`
	Level  Level             `json:"level"`
	Code   string            `json:"code"`
	Params map[string]string `json:"params"`
}

// Sample is the result of one check at one point in time.
type Sample struct {
	Key    string
	Level  Level // ok, warn or error
	Code   string
	Label  string // i18n code naming what is checked, e.g. lbl_disk
	Params map[string]string
	// Need is the number of consecutive samples required before a worse
	// level is reported (0 = the tracker's default).
	Need int
}

type checkState struct {
	reported    Level
	candidate   Level
	count       int
	transitions []time.Time
	flapping    bool
	lastBad     Sample // most recent sample that was not ok
}

// Tracker turns a stream of samples into events. A changed level is only
// reported after it was seen several times in a row (both ways), and a
// check that keeps changing is muted until it is stable again.
type Tracker struct {
	FailAfter  int
	FlapWindow time.Duration
	FlapMax    int
	Cooldown   time.Duration // minimum gap between one-off events of the same key

	now      func() time.Time
	states   map[string]*checkState
	lastShot map[string]time.Time
}

func NewTracker(failAfter int) *Tracker {
	return &Tracker{
		FailAfter:  failAfter,
		FlapWindow: 30 * time.Minute,
		FlapMax:    6,
		Cooldown:   10 * time.Minute,
		now:        time.Now,
		states:     map[string]*checkState{},
		lastShot:   map[string]time.Time{},
	}
}

// Reported returns the level currently reported for key (ok if unknown).
func (t *Tracker) Reported(key string) Level {
	if st, ok := t.states[key]; ok {
		return st.reported
	}
	return LevelOK
}

// Observe feeds one sample and returns the resulting events, if any.
func (t *Tracker) Observe(s Sample) []Event {
	now := t.now()
	st, ok := t.states[s.Key]
	if !ok {
		st = &checkState{reported: LevelOK, candidate: LevelOK}
		t.states[s.Key] = st
	}
	if s.Level != LevelOK {
		st.lastBad = s
	}

	var events []Event
	if st.flapping && len(st.transitions) > 0 && now.Sub(st.transitions[len(st.transitions)-1]) >= t.FlapWindow {
		st.flapping = false
		st.transitions = nil
		events = append(events, t.event(now, s.Key, LevelInfo, "flapping_end", labelParams(s)))
		if st.reported != LevelOK {
			events = append(events, t.stateEvent(now, st.reported, st.lastBad))
		}
	}

	if s.Level == st.reported {
		st.candidate, st.count = st.reported, 0
		return events
	}
	if s.Level != st.candidate {
		st.candidate, st.count = s.Level, 0
	}
	st.count++
	needed := t.FailAfter
	if s.Level.rank() > st.reported.rank() && s.Need > needed {
		needed = s.Need
	}
	if st.count < needed {
		return events
	}

	st.reported, st.count = s.Level, 0
	st.transitions = append(st.transitions, now)
	cut := 0
	for cut < len(st.transitions) && now.Sub(st.transitions[cut]) > t.FlapWindow {
		cut++
	}
	st.transitions = st.transitions[cut:]

	if st.flapping {
		return events
	}
	if len(st.transitions) >= t.FlapMax {
		st.flapping = true
		return append(events, t.event(now, s.Key, LevelWarn, "flapping", labelParams(s)))
	}
	return append(events, t.stateEvent(now, s.Level, s))
}

// OneShot reports a one-off happening (e.g. a restart), at most once per
// Cooldown per key, and not while the key is flapping.
func (t *Tracker) OneShot(key string, level Level, code string, params map[string]string) []Event {
	now := t.now()
	if st, ok := t.states[key]; ok && st.flapping {
		return nil
	}
	if last, ok := t.lastShot[key+"|"+code]; ok && now.Sub(last) < t.Cooldown {
		return nil
	}
	t.lastShot[key+"|"+code] = now
	return []Event{t.event(now, key, level, code, params)}
}

func (t *Tracker) stateEvent(now time.Time, level Level, s Sample) Event {
	if level == LevelOK {
		return t.event(now, s.Key, LevelInfo, "recovered", labelParams(s))
	}
	return t.event(now, s.Key, level, s.Code, s.Params)
}

func (t *Tracker) event(now time.Time, key string, level Level, code string, params map[string]string) Event {
	return Event{Time: now, Key: key, Level: level, Code: code, Params: params}
}

func labelParams(s Sample) map[string]string {
	p := make(map[string]string, len(s.Params)+1)
	for k, v := range s.Params {
		p[k] = v
	}
	p["label"] = s.Label
	return p
}
