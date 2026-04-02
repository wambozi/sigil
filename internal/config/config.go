// Package config loads and merges sigild's file-based TOML configuration.
// File values are defaults; CLI flags passed by the caller always win.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// DefaultFleetEndpoint is the canonical fleet reporting URL.
const DefaultFleetEndpoint = "https://fleet.sigil.dev/api/v1"

// DefaultCloudSyncURL is the canonical cloud sync ingest URL.
const DefaultCloudSyncURL = "https://ingest.sigil.cloud/api/v1"

// Config holds every tunable parameter for sigild.
// Zero values mean "use the built-in default" so callers can detect which
// fields were actually set by the file.
type Config struct {
	Daemon    DaemonConfig            `toml:"daemon"`
	Notifier  NotifierConfig          `toml:"notifier"`
	Inference InferenceConfig         `toml:"inference"`
	ML        MLConfig                `toml:"ml"`
	Plugins   map[string]PluginConfig `toml:"plugins"`
	Retention RetentionConfig         `toml:"retention"`
	Schedule  ScheduleConfig          `toml:"schedule"`
	Fleet     FleetConfig             `toml:"fleet"`
	Network   NetworkConfig           `toml:"network"`
	Cloud     CloudConfig             `toml:"cloud"`
	CloudSync CloudSyncConfig         `toml:"cloud_sync"`
	Sync      SyncConfig              `toml:"sync"`
}

// PluginConfig defines a single plugin's configuration.
type PluginConfig struct {
	Enabled      bool              `toml:"enabled" json:"enabled"`
	Binary       string            `toml:"binary" json:"binary"`
	Daemon       bool              `toml:"daemon" json:"daemon"`
	PollInterval string            `toml:"poll_interval" json:"poll_interval"`
	HealthURL    string            `toml:"health_url" json:"health_url"`
	Env          map[string]string `toml:"env" json:"env,omitempty"`
}

// MLConfig configures the ML prediction sidecar.
type MLConfig struct {
	Mode         string        `toml:"mode" json:"mode"`
	RetrainEvery int           `toml:"retrain_every" json:"retrain_every"`
	Local        MLLocalConfig `toml:"local" json:"local"`
	Cloud        MLCloudConfig `toml:"cloud" json:"cloud"`
}

// MLLocalConfig configures the local sigil-ml sidecar.
type MLLocalConfig struct {
	Enabled   bool   `toml:"enabled" json:"enabled"`
	ServerURL string `toml:"server_url" json:"server_url"`
	ServerBin string `toml:"server_bin" json:"server_bin"`
}

// MLCloudConfig configures the cloud ML API.
type MLCloudConfig struct {
	Enabled bool   `toml:"enabled" json:"enabled"`
	BaseURL string `toml:"base_url" json:"base_url"`
	APIKey  string `toml:"api_key" json:"api_key"`
}

// CloudConfig holds cloud tier and authentication settings.
type CloudConfig struct {
	Tier   string `toml:"tier" json:"tier"`
	APIKey string `toml:"api_key" json:"api_key"`
	OrgID  string `toml:"org_id" json:"org_id"`
}

// CloudSyncConfig controls the sync agent behavior.
type CloudSyncConfig struct {
	Enabled      *bool  `toml:"enabled" json:"enabled"`
	APIURL       string `toml:"api_url" json:"api_url"`
	BatchSize    int    `toml:"batch_size" json:"batch_size"`
	PollInterval string `toml:"poll_interval" json:"poll_interval"`
}

// IsEnabled returns whether cloud sync is enabled (defaults to false if unset).
func (c CloudSyncConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return false
	}
	return *c.Enabled
}

// NetworkConfig controls the optional TCP listener.
type NetworkConfig struct {
	Enabled            bool     `toml:"enabled" json:"enabled"`
	Bind               string   `toml:"bind" json:"bind"`
	Port               int      `toml:"port" json:"port"`
	AllowedCredentials []string `toml:"allowed_credentials" json:"allowed_credentials"`
}

// DaemonConfig covers process-level settings.
type DaemonConfig struct {
	LogLevel          string   `toml:"log_level" json:"log_level"`
	WatchDirs         []string `toml:"watch_dirs" json:"watch_dirs"`
	RepoDirs          []string `toml:"repo_dirs" json:"repo_dirs"`
	IgnorePatterns    []string `toml:"ignore_patterns" json:"ignore_patterns"`
	DBPath            string   `toml:"db_path" json:"db_path"`
	SocketPath        string   `toml:"socket_path" json:"socket_path"`
	MaxWatches        int      `toml:"max_watches" json:"max_watches"`
	ActuationsEnabled *bool    `toml:"actuations_enabled" json:"actuations_enabled"`
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
	Level           *int        `toml:"level" json:"level"`
	DigestTime      string      `toml:"digest_time" json:"digest_time"`
	DND             DNDSchedule `toml:"dnd" json:"dnd"`
	MutedCategories []string    `toml:"muted_categories" json:"muted_categories"`
}

// DNDSchedule defines a Do Not Disturb window.
type DNDSchedule struct {
	Enabled bool     `toml:"enabled" json:"enabled"`
	Start   string   `toml:"start" json:"start"`
	End     string   `toml:"end" json:"end"`
	Days    []string `toml:"days" json:"days"`
}

// LevelOrDefault returns the notification level, defaulting to 2 (Ambient).
func (n NotifierConfig) LevelOrDefault() int {
	if n.Level == nil {
		return 2
	}
	return *n.Level
}

// ScheduleConfig controls analysis timing.
type ScheduleConfig struct {
	AnalyzeEvery string `toml:"analyze_every" json:"analyze_every"`
}

// InferenceConfig configures the inference engine backends.
type InferenceConfig struct {
	Mode  string               `toml:"mode" json:"mode"`
	Local InferenceLocalConfig `toml:"local" json:"local"`
	Cloud InferenceCloudConfig `toml:"cloud" json:"cloud"`
}

// InferenceLocalConfig configures the local inference backend.
type InferenceLocalConfig struct {
	Enabled   bool   `toml:"enabled" json:"enabled"`
	ServerURL string `toml:"server_url" json:"server_url"`
	ServerBin string `toml:"server_bin" json:"server_bin"`
	ModelPath string `toml:"model_path" json:"model_path"`
	ModelName string `toml:"model_name" json:"model_name"`
	CtxSize   int    `toml:"ctx_size" json:"ctx_size"`
	GPULayers int    `toml:"gpu_layers" json:"gpu_layers"`
}

// InferenceCloudConfig configures the cloud inference backend.
type InferenceCloudConfig struct {
	Enabled  bool   `toml:"enabled" json:"enabled"`
	Provider string `toml:"provider" json:"provider"`
	BaseURL  string `toml:"base_url" json:"base_url"`
	APIKey   string `toml:"api_key" json:"api_key"`
	Model    string `toml:"model" json:"model"`
}

// RetentionConfig controls how long raw data is kept.
type RetentionConfig struct {
	RawEventDays int `toml:"raw_event_days" json:"raw_event_days"`
}

// FleetConfig controls the Fleet Reporter subsystem.
type FleetConfig struct {
	Enabled  bool   `toml:"enabled" json:"enabled"`
	Endpoint string `toml:"endpoint" json:"endpoint"`
	Interval string `toml:"interval" json:"interval"`
	NodeID   string `toml:"node_id" json:"node_id"`
}

// SyncConfig controls the Sync Agent that streams SQLite changes to the cloud.
type SyncConfig struct {
	Enabled  bool   `toml:"enabled"`
	APIURL   string `toml:"api_url"`
	APIKey   string `toml:"api_key"`
	Interval string `toml:"interval"`   // duration string, default "5s"
	Batch    int    `toml:"batch_size"` // rows per batch, default 100
}

// ApplyDefaults fills in zero-value fields that have well-known defaults.
// Call after Load to ensure fleet-enabled configs get the production endpoint.
func (c *Config) ApplyDefaults() {
	if c.Fleet.Endpoint == "" && c.Fleet.Enabled {
		c.Fleet.Endpoint = DefaultFleetEndpoint
	}
}

// Defaults returns a Config populated with sensible built-in values.
// This is what the daemon uses when no config file exists.
func Defaults() *Config {
	return &Config{
		Daemon: DaemonConfig{
			LogLevel: "info",
		},
		Notifier: NotifierConfig{
			DigestTime: "09:00",
		},
		Inference: InferenceConfig{
			Mode: "localfirst",
		},
		Retention: RetentionConfig{
			RawEventDays: 90,
		},
		Fleet: FleetConfig{
			Endpoint: DefaultFleetEndpoint,
		},
		CloudSync: CloudSyncConfig{
			APIURL: DefaultCloudSyncURL,
		},
	}
}

// DefaultPath returns the canonical config file location, respecting XDG_CONFIG_HOME.
// On Windows, uses %APPDATA% (Roaming) as the config base.
func DefaultPath() string {
	if runtime.GOOS == "windows" {
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		return filepath.Join(appdata, "sigil", "config.toml")
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".config")
	}
	return filepath.Join(base, "sigil", "config.toml")
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

// Marshal serializes a Config to TOML bytes.
func Marshal(cfg *Config) ([]byte, error) {
	return toml.Marshal(cfg)
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
	if len(src.Daemon.IgnorePatterns) > 0 {
		dst.Daemon.IgnorePatterns = src.Daemon.IgnorePatterns
	}
	if src.Daemon.DBPath != "" {
		dst.Daemon.DBPath = expandHome(src.Daemon.DBPath)
	}
	if src.Daemon.SocketPath != "" {
		dst.Daemon.SocketPath = expandHome(src.Daemon.SocketPath)
	}
	if src.Daemon.MaxWatches != 0 {
		dst.Daemon.MaxWatches = src.Daemon.MaxWatches
	}

	// Notifier: *int pointer distinguishes absent from explicitly 0 (Silent).
	if src.Notifier.Level != nil {
		dst.Notifier.Level = src.Notifier.Level
	}
	if src.Notifier.DigestTime != "" {
		dst.Notifier.DigestTime = src.Notifier.DigestTime
	}

	// Schedule
	if src.Schedule.AnalyzeEvery != "" {
		dst.Schedule.AnalyzeEvery = src.Schedule.AnalyzeEvery
	}

	if src.Inference.Mode != "" {
		dst.Inference.Mode = src.Inference.Mode
	}
	if src.Inference.Local.Enabled {
		dst.Inference.Local.Enabled = true
	}
	if src.Inference.Local.ServerURL != "" {
		dst.Inference.Local.ServerURL = src.Inference.Local.ServerURL
	}
	if src.Inference.Local.ServerBin != "" {
		dst.Inference.Local.ServerBin = src.Inference.Local.ServerBin
	}
	if src.Inference.Local.ModelPath != "" {
		dst.Inference.Local.ModelPath = src.Inference.Local.ModelPath
	}
	if src.Inference.Local.ModelName != "" {
		dst.Inference.Local.ModelName = src.Inference.Local.ModelName
	}
	if src.Inference.Local.CtxSize != 0 {
		dst.Inference.Local.CtxSize = src.Inference.Local.CtxSize
	}
	if src.Inference.Local.GPULayers != 0 {
		dst.Inference.Local.GPULayers = src.Inference.Local.GPULayers
	}
	if src.Inference.Cloud.Enabled {
		dst.Inference.Cloud.Enabled = true
	}
	if src.Inference.Cloud.Provider != "" {
		dst.Inference.Cloud.Provider = src.Inference.Cloud.Provider
	}
	if src.Inference.Cloud.BaseURL != "" {
		dst.Inference.Cloud.BaseURL = src.Inference.Cloud.BaseURL
	}
	if src.Inference.Cloud.APIKey != "" {
		dst.Inference.Cloud.APIKey = src.Inference.Cloud.APIKey
	}
	if src.Inference.Cloud.Model != "" {
		dst.Inference.Cloud.Model = src.Inference.Cloud.Model
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

	if src.Network.Enabled {
		dst.Network.Enabled = true
	}
	if src.Network.Bind != "" {
		dst.Network.Bind = src.Network.Bind
	}
	if src.Network.Port != 0 {
		dst.Network.Port = src.Network.Port
	}
	if len(src.Network.AllowedCredentials) > 0 {
		dst.Network.AllowedCredentials = src.Network.AllowedCredentials
	}

	// Sync
	if src.Sync.Enabled {
		dst.Sync.Enabled = true
	}
	if src.Sync.APIURL != "" {
		dst.Sync.APIURL = src.Sync.APIURL
	}
	if src.Sync.APIKey != "" {
		dst.Sync.APIKey = src.Sync.APIKey
	}
	if src.Sync.Interval != "" {
		dst.Sync.Interval = src.Sync.Interval
	}
	if src.Sync.Batch != 0 {
		dst.Sync.Batch = src.Sync.Batch
	}

	// Plugins (map — just replace entirely if set in file)
	if len(src.Plugins) > 0 {
		dst.Plugins = src.Plugins
	}

	// ML
	if src.ML.Mode != "" {
		dst.ML.Mode = src.ML.Mode
	}
	if src.ML.RetrainEvery != 0 {
		dst.ML.RetrainEvery = src.ML.RetrainEvery
	}
	if src.ML.Local.Enabled {
		dst.ML.Local.Enabled = true
	}
	if src.ML.Local.ServerURL != "" {
		dst.ML.Local.ServerURL = src.ML.Local.ServerURL
	}
	if src.ML.Local.ServerBin != "" {
		dst.ML.Local.ServerBin = src.ML.Local.ServerBin
	}
	if src.ML.Cloud.Enabled {
		dst.ML.Cloud.Enabled = true
	}
	if src.ML.Cloud.BaseURL != "" {
		dst.ML.Cloud.BaseURL = src.ML.Cloud.BaseURL
	}
	if src.ML.Cloud.APIKey != "" {
		dst.ML.Cloud.APIKey = src.ML.Cloud.APIKey
	}

	// Cloud tier
	if src.Cloud.Tier != "" {
		dst.Cloud.Tier = src.Cloud.Tier
	}
	if src.Cloud.APIKey != "" {
		dst.Cloud.APIKey = src.Cloud.APIKey
	}
	if src.Cloud.OrgID != "" {
		dst.Cloud.OrgID = src.Cloud.OrgID
	}

	// Cloud sync
	if src.CloudSync.Enabled != nil {
		dst.CloudSync.Enabled = src.CloudSync.Enabled
	}
	if src.CloudSync.APIURL != "" {
		dst.CloudSync.APIURL = src.CloudSync.APIURL
	}
	if src.CloudSync.BatchSize != 0 {
		dst.CloudSync.BatchSize = src.CloudSync.BatchSize
	}
	if src.CloudSync.PollInterval != "" {
		dst.CloudSync.PollInterval = src.CloudSync.PollInterval
	}
}

// Save atomically writes the config to the given path as TOML.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// MaskKeys returns a copy of the config with sensitive fields masked.
func MaskKeys(cfg *Config) *Config {
	c := *cfg // shallow copy
	c.Inference.Cloud.APIKey = maskString(c.Inference.Cloud.APIKey)
	// Copy the plugins map to avoid mutating the original
	if len(c.Plugins) > 0 {
		masked := make(map[string]PluginConfig, len(c.Plugins))
		for k, v := range c.Plugins {
			env := make(map[string]string, len(v.Env))
			for ek, ev := range v.Env {
				if strings.Contains(strings.ToLower(ek), "key") || strings.Contains(strings.ToLower(ek), "token") || strings.Contains(strings.ToLower(ek), "secret") {
					env[ek] = maskString(ev)
				} else {
					env[ek] = ev
				}
			}
			v.Env = env
			masked[k] = v
		}
		c.Plugins = masked
	}
	return &c
}

func maskString(s string) string {
	if len(s) <= 4 {
		if s == "" {
			return ""
		}
		return "****"
	}
	return "****" + s[len(s)-4:]
}

func expandDirs(dirs []string) []string {
	out := make([]string, len(dirs))
	for i, d := range dirs {
		out[i] = expandHome(d)
	}
	return out
}
