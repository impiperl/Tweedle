package api

import (
	"time"

	"github.com/impiperl/Tweedle/internal/monitor"
	"github.com/impiperl/Tweedle/internal/store"
)

// aggregator averages the tick samples of one minute into one metric point.
type aggregator struct {
	minute         time.Time
	n              int
	cpu, mem, swap float64
	load, rx, tx   float64
	disks          map[string]float64
}

// add feeds a snapshot. When a new minute starts, the finished minute is
// returned (ok=true).
func (a *aggregator) add(s *monitor.Snapshot) (store.Metric, bool) {
	if s == nil || s.System == nil {
		return store.Metric{}, false
	}
	sys := s.System
	min := sys.Time.Truncate(time.Minute)
	var out store.Metric
	done := false
	if a.n > 0 && !min.Equal(a.minute) {
		n := float64(a.n)
		out = store.Metric{Time: a.minute, CPU: a.cpu / n, Mem: a.mem / n, Swap: a.swap / n,
			Load1: a.load / n, NetRx: a.rx / n, NetTx: a.tx / n, Disks: a.disks}
		done = true
		*a = aggregator{}
	}
	if a.n == 0 {
		a.minute = min
	}
	a.n++
	a.cpu += sys.CPUPct
	a.mem += sys.MemPct
	a.swap += sys.SwapPct
	a.load += sys.Load1
	a.rx += sys.NetRxRate
	a.tx += sys.NetTxRate
	a.disks = map[string]float64{}
	for _, d := range sys.Disks {
		a.disks[d.Mount] = d.UsedPct
	}
	return out, done
}

// downsample reduces pts to at most max points by averaging neighbours
// (disks: maximum of the bucket).
func downsample(pts []store.Metric, max int) []store.Metric {
	if len(pts) <= max {
		return pts
	}
	size := (len(pts) + max - 1) / max
	out := make([]store.Metric, 0, max)
	for i := 0; i < len(pts); i += size {
		end := i + size
		if end > len(pts) {
			end = len(pts)
		}
		b := pts[i:end]
		m := store.Metric{Time: b[0].Time, Disks: map[string]float64{}}
		for _, p := range b {
			m.CPU += p.CPU
			m.Mem += p.Mem
			m.Swap += p.Swap
			m.Load1 += p.Load1
			m.NetRx += p.NetRx
			m.NetTx += p.NetTx
			for k, v := range p.Disks {
				if v > m.Disks[k] {
					m.Disks[k] = v
				}
			}
		}
		n := float64(len(b))
		m.CPU, m.Mem, m.Swap, m.Load1, m.NetRx, m.NetTx = m.CPU/n, m.Mem/n, m.Swap/n, m.Load1/n, m.NetRx/n, m.NetTx/n
		out = append(out, m)
	}
	return out
}
