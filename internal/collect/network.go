package collect

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

// PodmanGateway returns the IPv4 gateway of a podman network, i.e. the
// address under which containers in that network reach the host
// (host.containers.internal).
func PodmanGateway(cmd []string, network string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := append(append([]string{}, cmd[1:]...),
		"network", "inspect", network, "--format", "{{range .Subnets}}{{.Gateway}} {{end}}")
	c := exec.CommandContext(ctx, cmd[0], args...)
	var stderr strings.Builder
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("podman network %s: %s", network, msg)
	}
	for _, f := range strings.Fields(string(out)) {
		if ip := net.ParseIP(f); ip != nil && ip.To4() != nil {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("podman network %s has no IPv4 gateway", network)
}
