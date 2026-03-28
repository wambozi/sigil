package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Defaults ---------------------------------------------------------------

func TestDefaults(t *testing.T) {
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"LogLevel", Defaults().Daemon.LogLevel, "info"},
		{"NotifierLevel", Defaults().Notifier.LevelOrDefault(), 2},
		{"DigestTime", Defaults().Notifier.DigestTime, "09:00"},
		{"InferenceMode", Defaults().Inference.Mode, "localfirst"},
		{"RawEventDays", Defaults().Retention.RawEventDays, 90},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("Defaults().%s = %v; want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

// --- DefaultPath ------------------------------------------------------------

func TestDefaultPath(t *testing.T) {
	t.Run("XDG set", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/custom/xdg")
		got := DefaultPath()
		want := "/custom/xdg/sigil/config.toml"
		if got != want {
			t.Errorf("DefaultPath() = %q; want %q", got, want)
		}
	})

	t.Run("XDG empty falls back to home dir", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		got := DefaultPath()
		if !strings.HasSuffix(got, filepath.Join("sigil", "config.toml")) {
			t.Errorf("DefaultPath() = %q; expected suffix sigil/config.toml", got)
		}
		if !filepath.IsAbs(got) {
			t.Errorf("DefaultPath() = %q; want absolute path", got)
		}
	})
}

// --- Load -------------------------------------------------------------------

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		toml    string // empty string means: no file
		wantErr bool
		check   func(t *testing.T, cfg *Config)
	}{
		{
			name: "missing file returns defaults",
			toml: "", // no file written — Load sees ErrNotExist
			check: func(t *testing.T, cfg *Config) {
				if cfg.Daemon.LogLevel != "info" {
					t.Errorf("LogLevel = %q, want %q", cfg.Daemon.LogLevel, "info")
				}
				if cfg.Notifier.LevelOrDefault() != 2 {
					t.Errorf("Notifier.LevelOrDefault() = %d, want 2", cfg.Notifier.LevelOrDefault())
				}
				if cfg.Inference.Mode != "localfirst" {
					t.Errorf("Inference.Mode = %q, want localfirst", cfg.Inference.Mode)
				}
				if cfg.Retention.RawEventDays != 90 {
					t.Errorf("RawEventDays = %d, want 90", cfg.Retention.RawEventDays)
				}
			},
		},
		{
			name: "partial file merges with defaults",
			toml: `
[daemon]
log_level = "debug"

[inference]
mode = "remote"
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Daemon.LogLevel != "debug" {
					t.Errorf("LogLevel = %q, want %q", cfg.Daemon.LogLevel, "debug")
				}
				if cfg.Inference.Mode != "remote" {
					t.Errorf("Inference.Mode = %q, want remote", cfg.Inference.Mode)
				}
				// Unset fields keep defaults.
				if cfg.Notifier.LevelOrDefault() != 2 {
					t.Errorf("Notifier.LevelOrDefault() = %d, want 2 (default)", cfg.Notifier.LevelOrDefault())
				}
				if cfg.Retention.RawEventDays != 90 {
					t.Errorf("RawEventDays = %d, want 90 (default)", cfg.Retention.RawEventDays)
				}
			},
		},
		{
			name: "full file overrides all defaults",
			toml: `
[daemon]
log_level = "warn"
watch_dirs = ["/home/user/code"]
repo_dirs = ["/home/user/code/myproject"]
db_path = "/tmp/test.db"
socket_path = "/tmp/test.sock"

[notifier]
level = 3
digest_time = "08:00"

[inference]
mode = "remote"

[inference.local]
enabled = true
server_url = "http://127.0.0.1:8081"

[inference.cloud]
enabled = true
provider = "openai"
base_url = "http://remote:9090"

[retention]
raw_event_days = 30
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Daemon.LogLevel != "warn" {
					t.Errorf("LogLevel = %q", cfg.Daemon.LogLevel)
				}
				if len(cfg.Daemon.WatchDirs) != 1 || cfg.Daemon.WatchDirs[0] != "/home/user/code" {
					t.Errorf("WatchDirs = %v", cfg.Daemon.WatchDirs)
				}
				if cfg.Notifier.LevelOrDefault() != 3 {
					t.Errorf("Notifier.LevelOrDefault() = %d, want 3", cfg.Notifier.LevelOrDefault())
				}
				if cfg.Notifier.DigestTime != "08:00" {
					t.Errorf("DigestTime = %q, want 08:00", cfg.Notifier.DigestTime)
				}
				if cfg.Inference.Mode != "remote" {
					t.Errorf("Inference.Mode = %q", cfg.Inference.Mode)
				}
				if !cfg.Inference.Local.Enabled {
					t.Error("Inference.Local.Enabled = false, want true")
				}
				if cfg.Inference.Local.ServerURL != "http://127.0.0.1:8081" {
					t.Errorf("Inference.Local.ServerURL = %q", cfg.Inference.Local.ServerURL)
				}
				if !cfg.Inference.Cloud.Enabled {
					t.Error("Inference.Cloud.Enabled = false, want true")
				}
				if cfg.Inference.Cloud.BaseURL != "http://remote:9090" {
					t.Errorf("Inference.Cloud.BaseURL = %q", cfg.Inference.Cloud.BaseURL)
				}
				if cfg.Retention.RawEventDays != 30 {
					t.Errorf("RawEventDays = %d, want 30", cfg.Retention.RawEventDays)
				}
			},
		},
		{
			name: "notifier level 0 (silent) is respected",
			toml: `
[notifier]
level = 0
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Notifier.LevelOrDefault() != 0 {
					t.Errorf("Notifier.LevelOrDefault() = %d, want 0 (silent)", cfg.Notifier.LevelOrDefault())
				}
			},
		},
		{
			name: "schedule analyze_every merges",
			toml: `
[schedule]
analyze_every = "5m"
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Schedule.AnalyzeEvery != "5m" {
					t.Errorf("Schedule.AnalyzeEvery = %q; want 5m", cfg.Schedule.AnalyzeEvery)
				}
			},
		},
		{
			name:    "invalid TOML returns error",
			toml:    `[daemon\nNOT VALID TOML`,
			wantErr: true,
		},
		{
			name: "fleet fields merge",
			toml: `
[fleet]
enabled = true
endpoint = "https://fleet.example.com"
interval = "30m"
node_id = "node-abc"
`,
			check: func(t *testing.T, cfg *Config) {
				if !cfg.Fleet.Enabled {
					t.Error("Fleet.Enabled = false; want true")
				}
				if cfg.Fleet.Endpoint != "https://fleet.example.com" {
					t.Errorf("Fleet.Endpoint = %q", cfg.Fleet.Endpoint)
				}
				if cfg.Fleet.Interval != "30m" {
					t.Errorf("Fleet.Interval = %q; want 30m", cfg.Fleet.Interval)
				}
				if cfg.Fleet.NodeID != "node-abc" {
					t.Errorf("Fleet.NodeID = %q; want node-abc", cfg.Fleet.NodeID)
				}
			},
		},
		{
			name: "network fields merge",
			toml: `
[network]
enabled = true
bind = "0.0.0.0"
port = 9090
allowed_credentials = ["user:pass"]
`,
			check: func(t *testing.T, cfg *Config) {
				if !cfg.Network.Enabled {
					t.Error("Network.Enabled = false; want true")
				}
				if cfg.Network.Bind != "0.0.0.0" {
					t.Errorf("Network.Bind = %q", cfg.Network.Bind)
				}
				if cfg.Network.Port != 9090 {
					t.Errorf("Network.Port = %d; want 9090", cfg.Network.Port)
				}
				if len(cfg.Network.AllowedCredentials) != 1 {
					t.Errorf("AllowedCredentials len = %d; want 1", len(cfg.Network.AllowedCredentials))
				}
			},
		},
		{
			name: "ml fields merge",
			toml: `
[ml]
mode = "local"
retrain_every = 10

[ml.local]
enabled = true
server_url = "http://127.0.0.1:9191"
server_bin = "/usr/bin/sigil-ml"

[ml.cloud]
enabled = true
base_url = "https://ml.cloud"
api_key = "secret"
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.ML.Mode != "local" {
					t.Errorf("ML.Mode = %q; want local", cfg.ML.Mode)
				}
				if cfg.ML.RetrainEvery != 10 {
					t.Errorf("ML.RetrainEvery = %d; want 10", cfg.ML.RetrainEvery)
				}
				if !cfg.ML.Local.Enabled {
					t.Error("ML.Local.Enabled = false; want true")
				}
				if cfg.ML.Local.ServerURL != "http://127.0.0.1:9191" {
					t.Errorf("ML.Local.ServerURL = %q", cfg.ML.Local.ServerURL)
				}
				if cfg.ML.Local.ServerBin != "/usr/bin/sigil-ml" {
					t.Errorf("ML.Local.ServerBin = %q", cfg.ML.Local.ServerBin)
				}
				if !cfg.ML.Cloud.Enabled {
					t.Error("ML.Cloud.Enabled = false; want true")
				}
				if cfg.ML.Cloud.BaseURL != "https://ml.cloud" {
					t.Errorf("ML.Cloud.BaseURL = %q", cfg.ML.Cloud.BaseURL)
				}
				if cfg.ML.Cloud.APIKey != "secret" {
					t.Errorf("ML.Cloud.APIKey = %q", cfg.ML.Cloud.APIKey)
				}
			},
		},
		{
			name: "plugins map merges",
			toml: `
[plugins.myplugin]
enabled = true
binary = "/usr/bin/myplugin"
daemon = true
`,
			check: func(t *testing.T, cfg *Config) {
				if len(cfg.Plugins) != 1 {
					t.Fatalf("Plugins len = %d; want 1", len(cfg.Plugins))
				}
				p, ok := cfg.Plugins["myplugin"]
				if !ok {
					t.Fatal("plugin 'myplugin' not found")
				}
				if !p.Enabled {
					t.Error("plugin.Enabled = false; want true")
				}
				if p.Binary != "/usr/bin/myplugin" {
					t.Errorf("plugin.Binary = %q", p.Binary)
				}
			},
		},
		{
			name: "retention field merges",
			toml: `
[retention]
raw_event_days = 14
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Retention.RawEventDays != 14 {
					t.Errorf("RawEventDays = %d; want 14", cfg.Retention.RawEventDays)
				}
			},
		},
		{
			name: "tilde expansion in daemon paths",
			toml: `
[daemon]
db_path = "~/sigil.db"
socket_path = "~/sigil.sock"
watch_dirs = ["~/projects"]
repo_dirs = ["~/repos"]
`,
			check: func(t *testing.T, cfg *Config) {
				home, _ := os.UserHomeDir()
				if cfg.Daemon.DBPath != filepath.Join(home, "sigil.db") {
					t.Errorf("DBPath = %q; want expanded tilde", cfg.Daemon.DBPath)
				}
				if cfg.Daemon.SocketPath != filepath.Join(home, "sigil.sock") {
					t.Errorf("SocketPath = %q; want expanded tilde", cfg.Daemon.SocketPath)
				}
				if len(cfg.Daemon.WatchDirs) != 1 || cfg.Daemon.WatchDirs[0] != filepath.Join(home, "projects") {
					t.Errorf("WatchDirs = %v; want expanded tilde", cfg.Daemon.WatchDirs)
				}
				if len(cfg.Daemon.RepoDirs) != 1 || cfg.Daemon.RepoDirs[0] != filepath.Join(home, "repos") {
					t.Errorf("RepoDirs = %v; want expanded tilde", cfg.Daemon.RepoDirs)
				}
			},
		},
		{
			name: "inference local all fields",
			toml: `
[inference.local]
enabled = true
server_url = "http://localhost:8080"
server_bin = "/usr/bin/llama"
model_path = "/models/llama.gguf"
model_name = "llama3"
ctx_size = 4096
gpu_layers = 32
`,
			check: func(t *testing.T, cfg *Config) {
				l := cfg.Inference.Local
				if !l.Enabled {
					t.Error("Inference.Local.Enabled = false; want true")
				}
				if l.ServerURL != "http://localhost:8080" {
					t.Errorf("ServerURL = %q", l.ServerURL)
				}
				if l.ServerBin != "/usr/bin/llama" {
					t.Errorf("ServerBin = %q", l.ServerBin)
				}
				if l.ModelPath != "/models/llama.gguf" {
					t.Errorf("ModelPath = %q", l.ModelPath)
				}
				if l.ModelName != "llama3" {
					t.Errorf("ModelName = %q", l.ModelName)
				}
				if l.CtxSize != 4096 {
					t.Errorf("CtxSize = %d; want 4096", l.CtxSize)
				}
				if l.GPULayers != 32 {
					t.Errorf("GPULayers = %d; want 32", l.GPULayers)
				}
			},
		},
		{
			name: "inference cloud all fields",
			toml: `
[inference.cloud]
enabled = true
provider = "anthropic"
base_url = "https://api.anthropic.com"
api_key = "sk-123"
model = "claude-3-5-haiku-latest"
`,
			check: func(t *testing.T, cfg *Config) {
				c := cfg.Inference.Cloud
				if !c.Enabled {
					t.Error("Inference.Cloud.Enabled = false; want true")
				}
				if c.Provider != "anthropic" {
					t.Errorf("Provider = %q", c.Provider)
				}
				if c.BaseURL != "https://api.anthropic.com" {
					t.Errorf("BaseURL = %q", c.BaseURL)
				}
				if c.APIKey != "sk-123" {
					t.Errorf("APIKey = %q", c.APIKey)
				}
				if c.Model != "claude-3-5-haiku-latest" {
					t.Errorf("Model = %q", c.Model)
				}
			},
		},
		{
			name: "daemon max_watches and ignore_patterns",
			toml: `
[daemon]
max_watches = 8192
ignore_patterns = ["*.tmp", "*.swp"]
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Daemon.MaxWatches != 8192 {
					t.Errorf("MaxWatches = %d; want 8192", cfg.Daemon.MaxWatches)
				}
				if len(cfg.Daemon.IgnorePatterns) != 2 {
					t.Errorf("IgnorePatterns len = %d; want 2", len(cfg.Daemon.IgnorePatterns))
				}
			},
		},
		{
			// merge() has no branch for the nil-able ActuationsEnabled pointer, so
			// the file value is decoded but not propagated. IsActuationsEnabled
			// returns the default (true) after a Load.
			name: "actuations_enabled false in file not propagated by merge",
			toml: `
[daemon]
actuations_enabled = false
`,
			check: func(t *testing.T, cfg *Config) {
				if !cfg.Daemon.IsActuationsEnabled() {
					t.Error("IsActuationsEnabled() = false; expected default true (merge does not propagate the nil-able pointer)")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")

			if tc.toml != "" {
				if err := os.WriteFile(path, []byte(tc.toml), 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				// Use a non-existent path so Load sees ErrNotExist.
				path = filepath.Join(dir, "nonexistent.toml")
			}

			cfg, err := Load(path)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, cfg)
			}
		})
	}
}

// TestLoad_readError verifies that a non-ErrNotExist OS error (e.g. passing a
// directory path) surfaces as an error rather than silently returning defaults.
func TestLoad_readError(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "notafile")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Load(subdir)
	if err == nil {
		t.Error("expected error reading a directory as config file; got nil")
	}
}

// --- expandHome -------------------------------------------------------------

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"tilde prefix expanded", "~/foo/bar", filepath.Join(home, "foo/bar")},
		{"absolute path unchanged", "/absolute/path", "/absolute/path"},
		{"relative path without tilde unchanged", "relative/path", "relative/path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandHome(tt.input)
			if got != tt.want {
				t.Errorf("expandHome(%q) = %q; want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- IsActuationsEnabled ----------------------------------------------------

func TestIsActuationsEnabled(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name string
		d    DaemonConfig
		want bool
	}{
		{"nil pointer defaults to true", DaemonConfig{}, true},
		{"explicit true", DaemonConfig{ActuationsEnabled: &trueVal}, true},
		{"explicit false", DaemonConfig{ActuationsEnabled: &falseVal}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.IsActuationsEnabled(); got != tt.want {
				t.Errorf("IsActuationsEnabled() = %v; want %v", got, tt.want)
			}
		})
	}
}

// --- CloudSyncConfig.IsEnabled ----------------------------------------------

func TestCloudSyncIsEnabled(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name string
		c    CloudSyncConfig
		want bool
	}{
		{"nil pointer defaults to false", CloudSyncConfig{}, false},
		{"explicit true", CloudSyncConfig{Enabled: &trueVal}, true},
		{"explicit false", CloudSyncConfig{Enabled: &falseVal}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.IsEnabled(); got != tt.want {
				t.Errorf("IsEnabled() = %v; want %v", got, tt.want)
			}
		})
	}
}

// --- Cloud & CloudSync merge ------------------------------------------------

func TestLoad_CloudConfig(t *testing.T) {
	tests := []struct {
		name  string
		toml  string
		check func(t *testing.T, cfg *Config)
	}{
		{
			name: "cloud tier, api_key, org_id merge",
			toml: `
[cloud]
tier = "team"
api_key = "sk-sigil-abc123"
org_id = "org-42"
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Cloud.Tier != "team" {
					t.Errorf("Cloud.Tier = %q; want team", cfg.Cloud.Tier)
				}
				if cfg.Cloud.APIKey != "sk-sigil-abc123" {
					t.Errorf("Cloud.APIKey = %q", cfg.Cloud.APIKey)
				}
				if cfg.Cloud.OrgID != "org-42" {
					t.Errorf("Cloud.OrgID = %q; want org-42", cfg.Cloud.OrgID)
				}
			},
		},
		{
			name: "cloud fields absent keep defaults",
			toml: `
[daemon]
log_level = "debug"
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Cloud.Tier != "" {
					t.Errorf("Cloud.Tier = %q; want empty", cfg.Cloud.Tier)
				}
				if cfg.Cloud.APIKey != "" {
					t.Errorf("Cloud.APIKey = %q; want empty", cfg.Cloud.APIKey)
				}
				if cfg.Cloud.OrgID != "" {
					t.Errorf("Cloud.OrgID = %q; want empty", cfg.Cloud.OrgID)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte(tc.toml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.check(t, cfg)
		})
	}
}

func TestLoad_CloudSyncConfig(t *testing.T) {
	tests := []struct {
		name  string
		toml  string
		check func(t *testing.T, cfg *Config)
	}{
		{
			name: "cloud_sync enabled true overrides nil",
			toml: `
[cloud_sync]
enabled = true
`,
			check: func(t *testing.T, cfg *Config) {
				if !cfg.CloudSync.IsEnabled() {
					t.Error("CloudSync.IsEnabled() = false; want true")
				}
			},
		},
		{
			name: "cloud_sync enabled false overrides nil",
			toml: `
[cloud_sync]
enabled = false
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.CloudSync.Enabled == nil {
					t.Fatal("CloudSync.Enabled is nil; want non-nil false")
				}
				if cfg.CloudSync.IsEnabled() {
					t.Error("CloudSync.IsEnabled() = true; want false")
				}
			},
		},
		{
			name: "cloud_sync absent leaves enabled nil",
			toml: `
[daemon]
log_level = "debug"
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.CloudSync.Enabled != nil {
					t.Errorf("CloudSync.Enabled = %v; want nil", *cfg.CloudSync.Enabled)
				}
				if cfg.CloudSync.IsEnabled() {
					t.Error("CloudSync.IsEnabled() = true; want false (nil defaults to false)")
				}
			},
		},
		{
			name: "cloud_sync api_url, batch_size, poll_interval merge",
			toml: `
[cloud_sync]
api_url = "https://custom.example.com/api/v1"
batch_size = 50
poll_interval = "30s"
`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.CloudSync.APIURL != "https://custom.example.com/api/v1" {
					t.Errorf("CloudSync.APIURL = %q", cfg.CloudSync.APIURL)
				}
				if cfg.CloudSync.BatchSize != 50 {
					t.Errorf("CloudSync.BatchSize = %d; want 50", cfg.CloudSync.BatchSize)
				}
				if cfg.CloudSync.PollInterval != "30s" {
					t.Errorf("CloudSync.PollInterval = %q; want 30s", cfg.CloudSync.PollInterval)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte(tc.toml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.check(t, cfg)
		})
	}
}

// --- Defaults populate fleet and cloud_sync URLs ----------------------------

func TestDefaults_FleetEndpoint(t *testing.T) {
	cfg := Defaults()
	if cfg.Fleet.Endpoint != DefaultFleetEndpoint {
		t.Errorf("Fleet.Endpoint = %q; want %q", cfg.Fleet.Endpoint, DefaultFleetEndpoint)
	}
}

func TestDefaults_CloudSyncAPIURL(t *testing.T) {
	cfg := Defaults()
	if cfg.CloudSync.APIURL != DefaultCloudSyncURL {
		t.Errorf("CloudSync.APIURL = %q; want %q", cfg.CloudSync.APIURL, DefaultCloudSyncURL)
	}
}
