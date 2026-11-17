package collect

import "testing"

func TestPodmanGateway(t *testing.T) {
	// The fake "podman" prints like `network inspect --format`; the real
	// arguments end up as $1… and are ignored.
	fake := func(out string) []string { return []string{"sh", "-c", "echo '" + out + "'", "sh"} }
	gw, err := PodmanGateway(fake("fd00::1 10.89.0.1 "), "mynet")
	if err != nil || gw != "10.89.0.1" {
		t.Fatalf("got %q, %v", gw, err)
	}
	if _, err := PodmanGateway(fake(""), "x"); err == nil {
		t.Fatal("expected error without IPv4 gateway")
	}
	if _, err := PodmanGateway([]string{"sh", "-c", "echo 'no such network' >&2; exit 125", "sh"}, "x"); err == nil ||
		err.Error() != "podman network x: no such network" {
		t.Fatalf("unexpected error: %v", err)
	}
}
