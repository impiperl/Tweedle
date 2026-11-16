package collect

import (
	"os"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

type ProcInfo struct {
	PID        int32   `json:"pid"`
	Name       string  `json:"name"`
	Cmdline    string  `json:"cmdline"`
	User       string  `json:"user,omitempty"`
	CreateTime int64   `json:"createTime"`
	CPUPct     float64 `json:"cpuPct"`
	RSS        uint64  `json:"rss"`
}

type cachedProc struct {
	p          *process.Process
	createTime int64
}

// ProcessCollector lists processes. It keeps the gopsutil process objects
// between calls because per-process CPU usage is measured against the
// previous call.
type ProcessCollector struct {
	cache map[int32]cachedProc
	self  int32
}

func NewProcessCollector() *ProcessCollector {
	return &ProcessCollector{cache: map[int32]cachedProc{}, self: int32(os.Getpid())}
}

// List returns all user-space processes (kernel threads and Tweedel itself
// are left out). withUser additionally resolves the owner, which is slower.
func (c *ProcessCollector) List(withUser bool) ([]ProcInfo, error) {
	pids, err := process.Pids()
	if err != nil {
		return nil, err
	}
	alive := make(map[int32]cachedProc, len(pids))
	out := make([]ProcInfo, 0, len(pids))
	for _, pid := range pids {
		if pid == c.self {
			continue
		}
		cp, ok := c.cache[pid]
		if !ok {
			p, err := process.NewProcess(pid)
			if err != nil {
				continue
			}
			cp = cachedProc{p: p}
		}
		ct, err := cp.p.CreateTime()
		if err != nil {
			continue // process is gone
		}
		if ok && ct != cp.createTime {
			// PID was reused by a new process: start fresh.
			p, err := process.NewProcess(pid)
			if err != nil {
				continue
			}
			cp = cachedProc{p: p}
		}
		cp.createTime = ct
		cmd, _ := cp.p.Cmdline()
		if strings.TrimSpace(cmd) == "" {
			continue // kernel thread
		}
		name, _ := cp.p.Name()
		info := ProcInfo{PID: pid, Name: name, Cmdline: cmd, CreateTime: ct}
		if withUser {
			info.User, _ = cp.p.Username()
		}
		alive[pid] = cp
		out = append(out, info)
	}
	c.cache = alive
	return out, nil
}

// Usage fills CPU and memory for the given process. CPU is relative to the
// previous call for the same process (0 on the first call), in percent of
// one core.
func (c *ProcessCollector) Usage(info *ProcInfo) {
	cp, ok := c.cache[info.PID]
	if !ok {
		return
	}
	if pct, err := cp.p.Percent(0); err == nil {
		info.CPUPct = pct
	}
	if m, err := cp.p.MemoryInfo(); err == nil {
		info.RSS = m.RSS
	}
}
