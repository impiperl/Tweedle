// Package collect reads the current state of the machine: system resources,
// processes and Podman containers.
package collect

import (
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

type DiskStat struct {
	Mount     string  `json:"mount"`
	Device    string  `json:"device"`
	FSType    string  `json:"fstype"`
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	UsedPct   float64 `json:"usedPct"`
	InodesPct float64 `json:"inodesPct"`
}

type System struct {
	Time      time.Time  `json:"time"`
	Hostname  string     `json:"hostname"`
	Uptime    uint64     `json:"uptime"`
	CPUPct    float64    `json:"cpuPct"`
	Cores     int        `json:"cores"`
	Load1     float64    `json:"load1"`
	Load5     float64    `json:"load5"`
	Load15    float64    `json:"load15"`
	MemTotal  uint64     `json:"memTotal"`
	MemUsed   uint64     `json:"memUsed"`
	MemPct    float64    `json:"memPct"`
	SwapTotal uint64     `json:"swapTotal"`
	SwapUsed  uint64     `json:"swapUsed"`
	SwapPct   float64    `json:"swapPct"`
	Disks     []DiskStat `json:"disks"`
	NetRxRate float64    `json:"netRxRate"` // bytes per second, physical interfaces
	NetTxRate float64    `json:"netTxRate"`
}

// SystemCollector keeps the state needed for rates (CPU, network) between calls.
type SystemCollector struct {
	disks    []string
	prevRx   uint64
	prevTx   uint64
	prevTime time.Time
}

// NewSystemCollector creates a collector. disks limits the checked mount
// points; empty means all real file systems.
func NewSystemCollector(disks []string) *SystemCollector {
	c := &SystemCollector{disks: disks}
	// Prime the CPU and network counters so the first real sample has a base.
	cpu.Percent(0, false)
	c.prevRx, c.prevTx = netTotals()
	c.prevTime = time.Now()
	return c
}

func (c *SystemCollector) Collect() (*System, error) {
	s := &System{Time: time.Now()}

	if info, err := host.Info(); err == nil {
		s.Hostname = info.Hostname
		s.Uptime = info.Uptime
	}
	if p, err := cpu.Percent(0, false); err == nil && len(p) > 0 {
		s.CPUPct = p[0]
	}
	if n, err := cpu.Counts(true); err == nil {
		s.Cores = n
	}
	if l, err := load.Avg(); err == nil {
		s.Load1, s.Load5, s.Load15 = l.Load1, l.Load5, l.Load15
	}
	vm, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}
	s.MemTotal = vm.Total
	// "Used" as the kernel sees it: everything that is not available.
	s.MemUsed = vm.Total - vm.Available
	if vm.Total > 0 {
		s.MemPct = float64(s.MemUsed) / float64(vm.Total) * 100
	}
	if sw, err := mem.SwapMemory(); err == nil {
		s.SwapTotal, s.SwapUsed = sw.Total, sw.Used
		if sw.Total > 0 {
			s.SwapPct = float64(sw.Used) / float64(sw.Total) * 100
		}
	}
	s.Disks = c.collectDisks()

	rx, tx := netTotals()
	if dt := s.Time.Sub(c.prevTime).Seconds(); dt > 0 && rx >= c.prevRx && tx >= c.prevTx {
		s.NetRxRate = float64(rx-c.prevRx) / dt
		s.NetTxRate = float64(tx-c.prevTx) / dt
	}
	c.prevRx, c.prevTx, c.prevTime = rx, tx, s.Time
	return s, nil
}

var realFS = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true,
	"vfat": true, "exfat": true, "f2fs": true, "jfs": true, "reiserfs": true, "ntfs": true,
}

var skipMountPrefixes = []string{"/var/lib/containers", "/run/containers", "/proc", "/sys", "/var/lib/docker"}

func (c *SystemCollector) collectDisks() []DiskStat {
	var mounts []disk.PartitionStat
	if len(c.disks) > 0 {
		for _, m := range c.disks {
			mounts = append(mounts, disk.PartitionStat{Mountpoint: m})
		}
	} else {
		parts, err := disk.Partitions(false)
		if err != nil {
			return nil
		}
		// One entry per device: bind mounts of the same device are skipped,
		// the shortest mount point wins.
		byDevice := map[string]disk.PartitionStat{}
		for _, p := range parts {
			if !realFS[p.Fstype] || hasAnyPrefix(p.Mountpoint, skipMountPrefixes) {
				continue
			}
			if old, ok := byDevice[p.Device]; !ok || len(p.Mountpoint) < len(old.Mountpoint) {
				byDevice[p.Device] = p
			}
		}
		for _, p := range byDevice {
			mounts = append(mounts, p)
		}
	}
	var out []DiskStat
	for _, p := range mounts {
		u, err := disk.Usage(p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		out = append(out, DiskStat{
			Mount: p.Mountpoint, Device: p.Device, FSType: firstNonEmpty(p.Fstype, u.Fstype),
			Total: u.Total, Used: u.Used, UsedPct: u.UsedPercent, InodesPct: u.InodesUsedPercent,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mount < out[j].Mount })
	return out
}

var virtualIfPrefixes = []string{"lo", "veth", "podman", "cni", "docker", "br-", "virbr", "vnet", "tun", "tap", "wg"}

// netTotals sums the bytes of the physical interfaces.
func netTotals() (rx, tx uint64) {
	ifs, err := net.IOCounters(true)
	if err != nil {
		return 0, 0
	}
	for _, i := range ifs {
		if hasAnyPrefix(i.Name, virtualIfPrefixes) {
			continue
		}
		rx += i.BytesRecv
		tx += i.BytesSent
	}
	return rx, tx
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
