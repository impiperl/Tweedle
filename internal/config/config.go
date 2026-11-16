// Package config loads and saves the Tweedle configuration file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultPath is where the service looks for its configuration.
const DefaultPath = "/etc/tweedle/config.yaml"

// Limit is a threshold pair in percent (or absolute for load per core).
// A value of 0 disables that level. For, if set, is how long the value has
// to stay above the limit before an alarm is raised.
type Limit struct {
	Warn  float64       `yaml:"warn"`
	Error float64       `yaml:"error"`
	For   time.Duration `yaml:"for,omitempty"`
}

type Thresholds struct {
	CPU         Limit `yaml:"cpu"`
	Memory      Limit `yaml:"memory"`
	Swap        Limit `yaml:"swap"`
	Disk        Limit `yaml:"disk"`
	Inodes      Limit `yaml:"inodes"`
	LoadPerCore Limit `yaml:"load_per_core"`
}

// Process describes one watched process. It is recognised by its process
// name (as in /proc/<pid>/status) and, optionally, a part of its command line.
type Process struct {
	Name     string `yaml:"name"`
	Exe      string `yaml:"exe"`
	Cmdline  string `yaml:"cmdline,omitempty"`
	MinCount int    `yaml:"min_count"`
}

type Container struct {
	Name string `yaml:"name"`
}

// CertTarget is a TLS certificate to watch: either a server (Host, as
// host:port) or a PEM file (File).
type CertTarget struct {
	Name string `yaml:"name"`
	Host string `yaml:"host,omitempty"`
	File string `yaml:"file,omitempty"`
	// Verify checks chain and host name (default true). Set to false for
	// self-made certificates such as mkcert.
	Verify *bool `yaml:"verify,omitempty"`
}

// ShouldVerify reports whether chain and host name are verified.
func (t CertTarget) ShouldVerify() bool { return t.Verify == nil || *t.Verify }

type Certs struct {
	CheckEvery time.Duration `yaml:"check_every"`
	WarnDays   int           `yaml:"warn_days"`
	ErrorDays  int           `yaml:"error_days"`
	Targets    []CertTarget  `yaml:"targets"`
}

type Retention struct {
	Metrics time.Duration `yaml:"metrics"`
	Events  time.Duration `yaml:"events"`
}

type Push struct {
	Enabled bool `yaml:"enabled"`
	// AccessToken is only needed if "enhanced push security" is switched on
	// in the Expo account.
	AccessToken string `yaml:"access_token,omitempty"`
	// Debounce collects events for this long so a burst becomes one message.
	Debounce time.Duration `yaml:"debounce"`
}

type Config struct {
	// Name is shown in the app and in every push message.
	Name string `yaml:"name"`
	// Listen is the address of the HTTP API. nginx forwards /mon/ to it.
	Listen string `yaml:"listen"`
	// PublicURL is how the app reaches this server (shown in the pairing QR
	// code), e.g. https://gaistnet.com/mon
	PublicURL string `yaml:"public_url"`
	// DataDir holds the SQLite database.
	DataDir   string    `yaml:"data_dir"`
	Retention Retention `yaml:"retention"`
	// Lang is used for terminal output (de or en).
	Lang     string        `yaml:"lang"`
	Interval time.Duration `yaml:"interval"`
	// FailAfter is the number of consecutive bad samples before an alarm,
	// and of good samples before the all-clear.
	FailAfter int `yaml:"fail_after"`
	// PodmanCmd is the command used to query containers.
	PodmanCmd []string `yaml:"podman_cmd"`
	// Disks lists the mount points to check; empty means all real file systems.
	Disks      []string    `yaml:"disks,omitempty"`
	Thresholds Thresholds  `yaml:"thresholds"`
	Processes  []Process   `yaml:"processes"`
	Containers []Container `yaml:"containers"`
	Certs      Certs       `yaml:"certs"`
	Push       Push        `yaml:"push"`
}

// Default returns a configuration with the agreed default values.
func Default() *Config {
	host, _ := os.Hostname()
	return &Config{
		Name:      host,
		Listen:    "127.0.0.1:9300",
		DataDir:   "/var/lib/tweedle",
		Retention: Retention{Metrics: 7 * 24 * time.Hour, Events: 90 * 24 * time.Hour},
		Lang:      "de",
		Interval:  15 * time.Second,
		FailAfter: 3,
		PodmanCmd: []string{"podman"},
		Thresholds: Thresholds{
			CPU:         Limit{Warn: 90, For: 5 * time.Minute},
			Memory:      Limit{Warn: 90, Error: 95},
			Swap:        Limit{Warn: 50, Error: 80},
			Disk:        Limit{Warn: 80, Error: 90},
			Inodes:      Limit{Warn: 80, Error: 90},
			LoadPerCore: Limit{Warn: 2},
		},
		Certs: Certs{CheckEvery: time.Hour, WarnDays: 14, ErrorDays: 3},
		Push:  Push{Enabled: true, Debounce: 3 * time.Second},
	}
}

// Load reads the file at path. Fields missing in the file keep their defaults.
func Load(path string) (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// LoadOrDefault behaves like Load but returns the defaults if the file does not exist.
func LoadOrDefault(path string) (*Config, error) {
	cfg, err := Load(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	return cfg, err
}

func (c *Config) Validate() error {
	if c.Interval < time.Second {
		return fmt.Errorf("interval must be at least 1s")
	}
	if c.FailAfter < 1 {
		return fmt.Errorf("fail_after must be at least 1")
	}
	if len(c.PodmanCmd) == 0 {
		return fmt.Errorf("podman_cmd must not be empty")
	}
	if c.Lang != "de" && c.Lang != "en" {
		return fmt.Errorf("lang must be de or en")
	}
	if c.Listen == "" || c.DataDir == "" {
		return fmt.Errorf("listen and data_dir are required")
	}
	if c.Retention.Metrics < time.Hour || c.Retention.Events < time.Hour {
		return fmt.Errorf("retention values must be at least 1h")
	}
	if c.Certs.CheckEvery < time.Minute {
		return fmt.Errorf("certs.check_every must be at least 1m")
	}
	certNames := map[string]bool{}
	for i, t := range c.Certs.Targets {
		if t.Name == "" || (t.Host == "") == (t.File == "") {
			return fmt.Errorf("certs.targets[%d]: name and exactly one of host or file are required", i)
		}
		if certNames[t.Name] {
			return fmt.Errorf("certs.targets: name %q used twice", t.Name)
		}
		certNames[t.Name] = true
	}
	seen := map[string]bool{}
	for i, p := range c.Processes {
		if p.Name == "" || p.Exe == "" {
			return fmt.Errorf("processes[%d]: name and exe are required", i)
		}
		if seen[p.Name] {
			return fmt.Errorf("processes: name %q used twice", p.Name)
		}
		seen[p.Name] = true
		if p.MinCount < 1 {
			c.Processes[i].MinCount = 1
		}
	}
	return nil
}

const header = `# Tweedle configuration. Created by "tweedle setup", may be edited by hand.
# After a change: sudo systemctl restart tweedle
`

// DBPath is the location of the SQLite database.
func (c *Config) DBPath() string { return filepath.Join(c.DataDir, "tweedle.db") }

// Save writes the configuration to path, creating the directory if needed.
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(header), data...), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
