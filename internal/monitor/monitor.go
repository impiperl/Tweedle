// Package monitor runs the checks periodically and turns results into events.
package monitor

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/impiperl/Tweedle/internal/collect"
	"github.com/impiperl/Tweedle/internal/config"
)

// Notifier receives every event (terminal log, later push and storage).
type Notifier interface {
	Notify(Event)
}

type ProcStatus struct {
	Name     string  `json:"name"`
	Exe      string  `json:"exe"`
	Cmdline  string  `json:"cmdline,omitempty"`
	MinCount int     `json:"minCount"`
	Count    int     `json:"count"`
	PIDs     []int32 `json:"pids"`
	CPUPct   float64 `json:"cpuPct"`
	RSS      uint64  `json:"rss"`
	Level    Level   `json:"level"`
}

type ContainerStatus struct {
	collect.ContainerInfo
	Found bool  `json:"found"`
	Level Level `json:"level"`
}

// Snapshot is the complete current state, as shown by "status" and the app.
type Snapshot struct {
	Server     string            `json:"server"`
	System     *collect.System   `json:"system"`
	Processes  []ProcStatus      `json:"processes"`
	Containers []ContainerStatus `json:"containers"`
	Certs      []CertStatus      `json:"certs"`
	PodmanErr  string            `json:"podmanError,omitempty"`
	Levels     map[string]Level  `json:"levels"` // reported level per check key
}

type Monitor struct {
	cfg       *config.Config
	sys       *collect.SystemCollector
	procs     *collect.ProcessCollector
	tracker   *Tracker
	certs     *certChecker
	notifiers []Notifier

	lastPIDs  map[string]map[int32]int64 // process name → pid → create time, last time it was seen running
	lastStart map[string]int64           // container name → StartedAt

	// OnTick, if set, is called with every new snapshot (before events are emitted).
	OnTick func(*Snapshot)

	mu   sync.RWMutex
	snap *Snapshot
}

func New(cfg *config.Config, notifiers ...Notifier) *Monitor {
	return &Monitor{
		cfg:       cfg,
		sys:       collect.NewSystemCollector(cfg.Disks),
		procs:     collect.NewProcessCollector(),
		tracker:   NewTracker(cfg.FailAfter),
		certs:     newCertChecker(cfg.Certs),
		notifiers: notifiers,
		lastPIDs:  map[string]map[int32]int64{},
		lastStart: map[string]int64{},
	}
}

// Snapshot returns the state of the last tick (nil before the first one).
func (m *Monitor) Snapshot() *Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snap
}

// Run ticks every interval until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	m.emit(Event{Time: time.Now(), Key: "tweedle", Level: LevelInfo, Code: "started",
		Params: map[string]string{"server": m.cfg.Name}})
	// Give the CPU counters primed in New a moment as a base for the first sample.
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Second):
	}
	m.Tick()
	t := time.NewTicker(m.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Tick()
		}
	}
}

// Tick runs all checks once, emits resulting events and stores a snapshot.
func (m *Monitor) Tick() *Snapshot {
	snap := &Snapshot{Server: m.cfg.Name, Levels: map[string]Level{}}
	var samples []Sample
	var events []Event

	if sys, err := m.sys.Collect(); err == nil {
		snap.System = sys
		samples = append(samples, m.systemSamples(sys)...)
	}

	ps, ev := m.checkProcesses()
	snap.Processes = ps.status
	samples = append(samples, ps.samples...)
	events = append(events, ev...)

	if len(m.cfg.Containers) > 0 {
		cs, ev, err := m.checkContainers()
		if err != nil {
			snap.PodmanErr = err.Error()
			samples = append(samples, Sample{Key: "podman", Level: LevelError, Code: "podman_failed",
				Label: "lbl_podman", Params: map[string]string{"error": err.Error()}})
		} else {
			samples = append(samples, Sample{Key: "podman", Level: LevelOK, Label: "lbl_podman"})
			snap.Containers = cs.status
			samples = append(samples, cs.samples...)
			events = append(events, ev...)
		}
	}

	if len(m.cfg.Certs.Targets) > 0 {
		cs, cSamples := m.certs.check()
		snap.Certs = cs
		samples = append(samples, cSamples...)
	}

	for _, s := range samples {
		events = append(events, m.tracker.Observe(s)...)
	}
	for _, s := range samples {
		snap.Levels[s.Key] = m.tracker.Reported(s.Key)
	}
	for i := range snap.Processes {
		snap.Processes[i].Level = snap.Levels["proc:"+snap.Processes[i].Name]
	}
	for i := range snap.Containers {
		snap.Containers[i].Level = snap.Levels["container:"+snap.Containers[i].Name]
	}
	for i := range snap.Certs {
		snap.Certs[i].Level = snap.Levels["cert:"+snap.Certs[i].Name]
	}

	m.mu.Lock()
	m.snap = snap
	m.mu.Unlock()
	if m.OnTick != nil {
		m.OnTick(snap)
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].Level.rank() > events[j].Level.rank() })
	for _, e := range events {
		m.emit(e)
	}
	return snap
}

func (m *Monitor) emit(e Event) {
	e.Server = m.cfg.Name
	for _, n := range m.notifiers {
		n.Notify(e)
	}
}

// --- system ---------------------------------------------------------------

func (m *Monitor) systemSamples(s *collect.System) []Sample {
	th := m.cfg.Thresholds
	out := []Sample{
		m.limitSample("cpu", "cpu_high", "lbl_cpu", s.CPUPct, th.CPU, nil),
		m.limitSample("mem", "mem_high", "lbl_mem", s.MemPct, th.Memory, nil),
		m.limitSample("load", "load_high", "lbl_load", s.Load5/float64(max(s.Cores, 1)), th.LoadPerCore,
			map[string]string{"cores": strconv.Itoa(s.Cores), "value": fmt1(s.Load5)}),
	}
	// The load limit is per core; the message shows the absolute limit.
	if out[2].Level != LevelOK {
		out[2].Params["limit"] = fmt1(limitFor(out[2].Level, th.LoadPerCore) * float64(max(s.Cores, 1)))
	}
	if s.SwapTotal > 0 {
		out = append(out, m.limitSample("swap", "swap_high", "lbl_swap", s.SwapPct, th.Swap, nil))
	}
	for _, d := range s.Disks {
		p := map[string]string{"mount": d.Mount}
		out = append(out,
			m.limitSample("disk:"+d.Mount, "disk_high", "lbl_disk", d.UsedPct, th.Disk, p),
			m.limitSample("inodes:"+d.Mount, "inodes_high", "lbl_inodes", d.InodesPct, th.Inodes, copyMap(p)),
		)
	}
	return out
}

func (m *Monitor) limitSample(key, code, label string, v float64, l config.Limit, extra map[string]string) Sample {
	p := map[string]string{"value": fmt1(v)}
	for k, x := range extra {
		p[k] = x
	}
	lvl := LevelOK
	switch {
	case l.Error > 0 && v >= l.Error:
		lvl = LevelError
	case l.Warn > 0 && v >= l.Warn:
		lvl = LevelWarn
	}
	if lvl != LevelOK {
		if _, ok := extra["limit"]; !ok {
			p["limit"] = fmt1(limitFor(lvl, l))
		}
	}
	need := 0
	if l.For > 0 {
		need = int(math.Ceil(float64(l.For) / float64(m.cfg.Interval)))
	}
	return Sample{Key: key, Level: lvl, Code: code, Label: label, Params: p, Need: need}
}

func limitFor(lvl Level, l config.Limit) float64 {
	if lvl == LevelError {
		return l.Error
	}
	return l.Warn
}

// --- processes ------------------------------------------------------------

type checkResult[T any] struct {
	status  []T
	samples []Sample
}

func (m *Monitor) checkProcesses() (checkResult[ProcStatus], []Event) {
	var res checkResult[ProcStatus]
	var events []Event
	if len(m.cfg.Processes) == 0 {
		return res, nil
	}
	all, err := m.procs.List(false)
	if err != nil {
		return res, nil
	}
	for _, want := range m.cfg.Processes {
		st := ProcStatus{Name: want.Name, Exe: want.Exe, Cmdline: want.Cmdline, MinCount: want.MinCount}
		cur := map[int32]int64{}
		for i := range all {
			p := &all[i]
			if !Matches(want, p.Name, p.Cmdline) {
				continue
			}
			m.procs.Usage(p)
			st.Count++
			st.PIDs = append(st.PIDs, p.PID)
			st.CPUPct += p.CPUPct
			st.RSS += p.RSS
			cur[p.PID] = p.CreateTime
		}
		key := "proc:" + want.Name
		params := map[string]string{"what": want.Name, "count": strconv.Itoa(st.Count), "min": strconv.Itoa(want.MinCount)}
		lvl := LevelOK
		if st.Count < want.MinCount {
			lvl = LevelError
		}
		res.samples = append(res.samples, Sample{Key: key, Level: lvl, Code: "proc_down", Label: "lbl_proc", Params: params})

		// Restart: all processes seen last time are gone and new ones run.
		// Only reported while the process is not already known as down.
		if st.Count > 0 {
			if prev := m.lastPIDs[want.Name]; len(prev) > 0 && noneSurvived(prev, cur) && m.tracker.Reported(key) == LevelOK {
				events = append(events, m.tracker.OneShot(key, LevelWarn, "proc_restarted", map[string]string{"what": want.Name})...)
			}
			m.lastPIDs[want.Name] = cur
		}
		res.status = append(res.status, st)
	}
	return res, events
}

// Matches reports whether a process fits a watched-process entry.
func Matches(want config.Process, name, cmdline string) bool {
	return name == want.Exe && (want.Cmdline == "" || strings.Contains(cmdline, want.Cmdline))
}

func noneSurvived(prev, cur map[int32]int64) bool {
	for pid, ct := range prev {
		if c, ok := cur[pid]; ok && c == ct {
			return false
		}
	}
	return true
}

// --- containers -----------------------------------------------------------

func (m *Monitor) checkContainers() (checkResult[ContainerStatus], []Event, error) {
	var res checkResult[ContainerStatus]
	var events []Event
	list, err := collect.ListContainers(m.cfg.PodmanCmd)
	if err != nil {
		return res, nil, err
	}
	byName := map[string]collect.ContainerInfo{}
	for _, c := range list {
		byName[c.Name] = c
	}
	for _, want := range m.cfg.Containers {
		key := "container:" + want.Name
		params := map[string]string{"what": want.Name}
		c, found := byName[want.Name]
		st := ContainerStatus{ContainerInfo: c, Found: found}
		st.Name = want.Name
		s := Sample{Key: key, Level: LevelOK, Label: "lbl_container", Params: params}
		switch {
		case !found:
			s.Level, s.Code = LevelError, "container_missing"
		case c.State != "running":
			s.Level, s.Code = LevelError, "container_down"
			params["state"] = c.State
		case c.Health == "unhealthy":
			s.Level, s.Code = LevelWarn, "container_unhealthy"
		}
		res.samples = append(res.samples, s)

		if found && c.State == "running" {
			if prev, ok := m.lastStart[want.Name]; ok && prev != c.StartedAt && m.tracker.Reported(key) == LevelOK {
				events = append(events, m.tracker.OneShot(key, LevelWarn, "container_restarted", map[string]string{"what": want.Name})...)
			}
			m.lastStart[want.Name] = c.StartedAt
		}
		res.status = append(res.status, st)
	}
	return res, events, nil
}

// --- helpers --------------------------------------------------------------

func fmt1(v float64) string { return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64) }

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// HumanBytes formats a byte count as e.g. "3.2 GiB".
func HumanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
