package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type ContainerInfo struct {
	Name      string `json:"name"`
	Image     string `json:"image"`
	State     string `json:"state"`  // running, exited, created, paused …
	Status    string `json:"status"` // human readable, e.g. "Up 2 hours (healthy)"
	Health    string `json:"health"` // healthy, unhealthy, starting or empty
	Restarts  int    `json:"restarts"`
	StartedAt int64  `json:"startedAt"`
}

type podmanPS struct {
	Names     []string `json:"Names"`
	Image     string   `json:"Image"`
	State     string   `json:"State"`
	Status    string   `json:"Status"`
	Restarts  int      `json:"Restarts"`
	StartedAt int64    `json:"StartedAt"`
	IsInfra   bool     `json:"IsInfra"`
}

// ListContainers runs "<cmd> ps -a --format json" and returns all containers.
func ListContainers(cmd []string) ([]ContainerInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := append(append([]string{}, cmd[1:]...), "ps", "-a", "--format", "json")
	c := exec.CommandContext(ctx, cmd[0], args...)
	var stderr strings.Builder
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	var raw []podmanPS
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("podman output: %w", err)
	}
	res := make([]ContainerInfo, 0, len(raw))
	for _, r := range raw {
		if r.IsInfra || len(r.Names) == 0 {
			continue
		}
		res = append(res, ContainerInfo{
			Name: r.Names[0], Image: r.Image, State: r.State, Status: r.Status,
			Health: healthFromStatus(r.Status), Restarts: r.Restarts, StartedAt: r.StartedAt,
		})
	}
	return res, nil
}

func healthFromStatus(status string) string {
	switch {
	case strings.Contains(status, "(unhealthy)"):
		return "unhealthy"
	case strings.Contains(status, "(healthy)"):
		return "healthy"
	case strings.Contains(status, "(starting)"):
		return "starting"
	}
	return ""
}
