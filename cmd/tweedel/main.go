// Command tweedel is the monitoring agent: "setup" chooses what to watch,
// "serve" runs the checks, "status" prints the current state once.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/impiperl/Tweedle/internal/config"
	"github.com/impiperl/Tweedle/internal/i18n"
	"github.com/impiperl/Tweedle/internal/monitor"
	"github.com/impiperl/Tweedle/internal/setup"
)

var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `tweedel %s

  tweedel setup  [--config PATH]   choose processes and containers to watch
  tweedel serve  [--config PATH]   run the checks (systemd service)
  tweedel status [--config PATH]   print the current state once
  tweedel version

Default config: %s
`, version, config.DefaultPath)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath, "config file")
	_ = fs.Parse(args)

	var err error
	switch cmd {
	case "setup":
		err = runSetup(*cfgPath)
	case "serve":
		err = runServe(*cfgPath)
	case "status":
		err = runStatus(*cfgPath)
	case "version", "--version", "-v":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tweedel:", err)
		os.Exit(1)
	}
}

func runSetup(path string) error {
	cfg, err := config.LoadOrDefault(path)
	if err != nil {
		return err
	}
	out, ok, err := setup.Run(cfg)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println(i18n.T(cfg.Lang, "setup_aborted", nil))
		return nil
	}
	if err := out.Save(path); err != nil {
		return err
	}
	fmt.Println(i18n.T(cfg.Lang, "setup_saved", map[string]string{"path": path}))
	fmt.Println(i18n.T(cfg.Lang, "setup_restart", nil))
	return nil
}

func runServe(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m := monitor.New(cfg, &monitor.LogNotifier{Lang: cfg.Lang, Out: os.Stdout})
	m.Run(ctx)
	return nil
}

func runStatus(path string) error {
	cfg, err := config.LoadOrDefault(path)
	if err != nil {
		return err
	}
	// Status only shows the state; a single bad sample is shown as-is.
	cfg.FailAfter = 1
	for _, l := range []*config.Limit{&cfg.Thresholds.CPU, &cfg.Thresholds.Memory, &cfg.Thresholds.Swap,
		&cfg.Thresholds.Disk, &cfg.Thresholds.Inodes, &cfg.Thresholds.LoadPerCore} {
		l.For = 0
	}
	m := monitor.New(cfg)
	m.Tick() // primes per-process CPU
	time.Sleep(time.Second)
	printSnapshot(cfg.Lang, m.Tick())
	return nil
}

func printSnapshot(lang string, s *monitor.Snapshot) {
	t := func(code string) string { return i18n.T(lang, code, nil) }
	lvl := func(key string) string {
		l := s.Levels[key]
		if l == "" {
			l = monitor.LevelOK
		}
		return t("level_" + string(l))
	}
	fmt.Printf("%s\n\n", s.Server)
	if sys := s.System; sys != nil {
		fmt.Printf("  %-7s %-6s %5.1f %%   (%d cores)\n", "CPU", lvl("cpu"), sys.CPUPct, sys.Cores)
		fmt.Printf("  %-7s %-6s %.2f %.2f %.2f\n", "Load", lvl("load"), sys.Load1, sys.Load5, sys.Load15)
		fmt.Printf("  %-7s %-6s %5.1f %%   %s / %s\n", "RAM", lvl("mem"), sys.MemPct,
			monitor.HumanBytes(sys.MemUsed), monitor.HumanBytes(sys.MemTotal))
		if sys.SwapTotal > 0 {
			fmt.Printf("  %-7s %-6s %5.1f %%   %s / %s\n", "Swap", lvl("swap"), sys.SwapPct,
				monitor.HumanBytes(sys.SwapUsed), monitor.HumanBytes(sys.SwapTotal))
		}
		for _, d := range sys.Disks {
			fmt.Printf("  %-7s %-6s %5.1f %%   %s / %s   inodes %.1f %% %s   %s\n", "Disk", lvl("disk:"+d.Mount), d.UsedPct,
				monitor.HumanBytes(d.Used), monitor.HumanBytes(d.Total), d.InodesPct, lvl("inodes:"+d.Mount), d.Mount)
		}
		fmt.Printf("  %-7s        ↓ %s/s  ↑ %s/s\n", "Net", monitor.HumanBytes(uint64(sys.NetRxRate)), monitor.HumanBytes(uint64(sys.NetTxRate)))
		fmt.Printf("  %-7s        %s\n", "Uptime", (time.Duration(sys.Uptime) * time.Second).String())
	}
	fmt.Printf("\n%s\n", t("status_processes"))
	if len(s.Processes) == 0 {
		fmt.Println("  " + t("status_none"))
	}
	for _, p := range s.Processes {
		fmt.Printf("  %-6s %-24s %d/%d   CPU %5.1f %%   RSS %s\n", t("level_"+string(p.Level)), p.Name, p.Count, p.MinCount,
			p.CPUPct, monitor.HumanBytes(p.RSS))
	}
	fmt.Printf("\n%s\n", t("status_containers"))
	if s.PodmanErr != "" {
		fmt.Println("  podman: " + s.PodmanErr)
	} else if len(s.Containers) == 0 {
		fmt.Println("  " + t("status_none"))
	}
	sort.Slice(s.Containers, func(i, j int) bool { return s.Containers[i].Name < s.Containers[j].Name })
	for _, c := range s.Containers {
		fmt.Printf("  %-6s %-24s %s\n", t("level_"+string(c.Level)), c.Name, strings.TrimSpace(c.Status))
	}
}
