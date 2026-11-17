package config

import "testing"

func TestListenPodman(t *testing.T) {
	cases := []struct {
		listen        string
		network, port string
		ok, bad       bool
	}{
		{"127.0.0.1:9300", "", "", false, false},
		{"podman:mynet:9300", "mynet", "9300", true, false},
		{"podman:mynet", "", "", true, true},
		{"podman::9300", "", "", true, true},
		{"podman:mynet:99999", "", "", true, true},
	}
	for _, tc := range cases {
		c := Default()
		c.Listen = tc.listen
		n, p, ok, err := c.ListenPodman()
		if n != tc.network || p != tc.port || ok != tc.ok || (err != nil) != tc.bad {
			t.Errorf("%s: got %q %q %v %v", tc.listen, n, p, ok, err)
		}
		if (c.Validate() != nil) != tc.bad {
			t.Errorf("%s: Validate disagrees", tc.listen)
		}
	}
}
