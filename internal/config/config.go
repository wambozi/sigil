// Package config loads and merges aetherd's file-based TOML configuration.
// File values are defaults; CLI flags passed by the caller always win.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config holds every tunable parameter for aetherd.
// Zero values mean "use the built-in default" so callers can detect which
// fields were actually set by the file.
type Config struct {
	Daemon    DaemonConfig    `toml:"daemon"`
	Notifier  NotifierConfig  `toml:"notifier"`
	Cactus    CactusConfig    `toml:"cactus"`
	Retention RetentionConfig `toml:"retention"`
	Fleet     FleetConfig     `toml:"fleet"`
}

// DaemonConfig covers process-level settings.
type DaemonConfig struct {
	LogLevel          string   `toml:"log_level"`
	WatchDirs         []string `toml:"watch_dirs"`
	RepoDirs          []string `toml:"repo_dirs"`
	DBPath            string   `toml:"db_path"`
	SocketPath        string   `toml:"socket_path"`
	ActuationsEnabled *bool    `toml:"actuations_enabled"` // nil = default true
}

// IsActuationsEnabled returns whether actuations are enabled (defaults to true).
func (d DaemonConfig) IsActuationsEnabled() bool {
	if d.ActuationsEnabled == nil {
		return true
	}
	return *d.ActuationsEnabled
}

// NotifierConfig controls how suggestions are surfaced.
type NotifierConfig struct {
	Level      int    `toml:"level"`
	DigestTime string `toml:"digest_time"` // "HH:MM" in local time
}

// CactusConfig points at the Cactus inference endpoint.
type CactusConfig struct {
	URL            string `toml:"url"`
	RoutingMode    string `toml:"routing_mode"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
}

// RetentionConfig controls how long raw data is kept.
type RetentionConfig struct {
	RawEventDays int `toml:"raw_event_days"`
}

// FleetConfig controls the Fleet Reporter subsystem.
type FleetConfig struct {
	Enabled  bool   `toml:"enabled"`
	Endpoint string `toml:"endpoint"`
	Interval string `toml:"interval"` // duration string, default "1h"
	NodeID   string `toml:"node_id"`  // auto-generated if empty
}

// Defaults returns a Config populated with sensible built-in values.
// This is what the daemon uses when no config file exists.
func Defaults() *Config {
	return &Config{
		Daemon: DaemonConfig{
			LogLevel: "info",
		},
		Notifier: NotifierConfig{
			Level:      2, // LevelAmbient
			DigestTime: "09:00",
		},
		Cactus: CactusConfig{
			URL:            "http://127.0.0.1:8080",
			RoutingMode:    "localfirst",
			TimeoutSeconds: 60,
		},
		Retention: RetentionConfig{
			RawEventDays: 90,
		},
	}
}

// DefaultPath returns the canonical config file location, respecting XDG_CONFIG_HOME.
func DefaultPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".config")
	}
	return filepath.Join(base, "aether", "config.toml")
}

// Load reads the TOML file at path and merges it on top of built-in defaults.
// If the file does not exist, defaults are returned without error.
// An invalid TOML file returns an error.
func Load(path string) (*Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	// Decode into a temporary struct so zero values in the file don't silently
	// overwrite defaults (e.g. level=0 is valid, but an absent [notifier]
	// section should leave the default level intact).
	var file Config
	if err := toml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	merge(cfg, &file)
	return cfg, nil
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, p[2:])
	}
	return p
}

// merge overlays non-zero fields from src onto dst.
func merge(dst, src *Config) {
	if src.Daemon.LogLevel != "" {
		dst.Daemon.LogLevel = src.Daemon.LogLevel
	}
	if len(src.Daemon.WatchDirs) > 0 {
		dst.Daemon.WatchDirs = expandDirs(src.Daemon.WatchDirs)
	}
	if len(src.Daemon.RepoDirs) > 0 {
		dst.Daemon.RepoDirs = expandDirs(src.Daemon.RepoDirs)
	}
	if src.Daemon.DBPath != "" {
		dst.Daemon.DBPath = expandHome(src.Daemon.DBPath)
	}
	if src.Daemon.SocketPath != "" {
		dst.Daemon.SocketPath = expandHome(src.Daemon.SocketPath)
	}

	// Notifier: level 0 (Silent) is a valid non-default, so we use a sentinel.
	// We trust whatever the file sets.
	if src.Notifier.Level != 0 {
		dst.Notifier.Level = src.Notifier.Level
	}
	if src.Notifier.DigestTime != "" {
		dst.Notifier.DigestTime = src.Notifier.DigestTime
	}

	if src.Cactus.URL != "" {
		dst.Cactus.URL = src.Cactus.URL
	}
	if src.Cactus.RoutingMode != "" {
		dst.Cactus.RoutingMode = src.Cactus.RoutingMode
	}
	if src.Cactus.TimeoutSeconds != 0 {
		dst.Cactus.TimeoutSeconds = src.Cactus.TimeoutSeconds
	}

	if src.Retention.RawEventDays != 0 {
		dst.Retention.RawEventDays = src.Retention.RawEventDays
	}

	if src.Fleet.Enabled {
		dst.Fleet.Enabled = true
	}
	if src.Fleet.Endpoint != "" {
		dst.Fleet.Endpoint = src.Fleet.Endpoint
	}
	if src.Fleet.Interval != "" {
		dst.Fleet.Interval = src.Fleet.Interval
	}
	if src.Fleet.NodeID != "" {
		dst.Fleet.NodeID = src.Fleet.NodeID
	}
}

func expandDirs(dirs []string) []string {
	out := make([]string, len(dirs))
	for i, d := range dirs {
		out[i] = expandHome(d)
	}
	return out
}
