// Package config loads and saves the Tweedel configuration file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultPath is where the service looks for its configuration.
const DefaultPath = "/etc/tweedel/config.yaml"

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

type Config struct {
	// Name is shown in the app and in every push message.
	Name string `yaml:"name"`
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
}

// Default returns a configuration with the agreed default values.
func Default() *Config {
	host, _ := os.Hostname()
	return &Config{
		Name:      host,
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

const header = `# Tweedel configuration. Created by "tweedel setup", may be edited by hand.
# After a change: sudo systemctl restart tweedel
`

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
