// Package plugin provides a framework for extending sigild with external
// data sources.  Plugins are standalone processes (any language, preferably Go)
// that push events to sigild via an HTTP ingest endpoint.  sigild manages
// plugin lifecycles, health checks, and event normalization.
//
// Plugin contract:
//   - Plugin binary accepts --sigil-ingest-url and --config flags
//   - Plugin POSTs JSON events to the ingest URL
//   - Plugin exposes /health on its own port (optional, for sigild monitoring)
//   - Plugin exits cleanly on SIGTERM
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Event is the normalized envelope for all plugin-produced data.
type Event struct {
	Plugin      string         `json:"plugin"` // plugin name (e.g. "jira", "claude", "github")
	Kind        string         `json:"kind"`   // event type (e.g. "story_update", "ai_turn", "pr_status")
	Timestamp   time.Time      `json:"timestamp"`
	Correlation Correlation    `json:"correlation"` // links event to tasks/stories
	Payload     map[string]any `json:"payload"`     // plugin-specific data
}

// Correlation links plugin events to Sigil tasks and external systems.
type Correlation struct {
	StoryID  string `json:"story_id,omitempty"`  // e.g. "PROJ-123", "LIN-456"
	Branch   string `json:"branch,omitempty"`    // git branch
	RepoRoot string `json:"repo_root,omitempty"` // repository path
	TaskID   string `json:"task_id,omitempty"`   // sigil task ID
	PRID     string `json:"pr_id,omitempty"`     // pull request ID
}

// Config defines how a plugin is configured in sigild's config.toml.
type Config struct {
	Name         string            `toml:"name"` // plugin identifier
	Enabled      bool              `toml:"enabled"`
	Binary       string            `toml:"binary"`        // binary name (found via PATH) or absolute path
	Daemon       bool              `toml:"daemon"`        // true = run as long-lived process, false = hook-only
	PollInterval string            `toml:"poll_interval"` // for polling plugins (e.g. "5m")
	HealthURL    string            `toml:"health_url"`    // optional health check URL
	Env          map[string]string `toml:"env"`           // environment variables passed to plugin
}

// Instance represents a running plugin process managed by the Manager.
type Instance struct {
	Config  Config
	proc    *os.Process
	healthy bool
	mu      sync.Mutex
}

// Manager starts, stops, and health-checks plugin processes.
type Manager struct {
	plugins   map[string]*Instance
	ingestURL string // URL plugins POST events to
	log       *slog.Logger
	mu        sync.RWMutex
}

// NewManager creates a plugin Manager.
func NewManager(ingestURL string, log *slog.Logger) *Manager {
	return &Manager{
		plugins:   make(map[string]*Instance),
		ingestURL: ingestURL,
		log:       log,
	}
}

// Register adds a plugin configuration. Call before Start.
func (m *Manager) Register(cfg Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.plugins[cfg.Name] = &Instance{Config: cfg}
}

// Start launches all enabled plugins and begins health monitoring.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for name, inst := range m.plugins {
		if !inst.Config.Enabled {
			m.log.Info("plugin: skipping disabled", "plugin", name)
			continue
		}
		if !inst.Config.Daemon {
			m.log.Info("plugin: hook-only, not starting as daemon", "plugin", name)
			continue
		}
		if err := m.startPlugin(ctx, inst); err != nil {
			m.log.Warn("plugin: failed to start", "plugin", name, "err", err)
			continue
		}
		inst.mu.Lock()
		pid := 0
		if inst.proc != nil {
			pid = inst.proc.Pid
		}
		inst.mu.Unlock()
		m.log.Info("plugin: started", "plugin", name, "pid", pid)
	}

	// Start health monitor.
	go m.healthLoop(ctx)

	return nil
}

// Stop terminates all running plugin processes.
func (m *Manager) Stop() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for name, inst := range m.plugins {
		inst.mu.Lock()
		if inst.proc != nil {
			m.log.Info("plugin: stopping", "plugin", name)
			_ = signalTerm(inst.proc)
			done := make(chan struct{})
			go func() {
				_, _ = inst.proc.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = signalKill(inst.proc)
				<-done
			}
			inst.proc = nil
		}
		inst.mu.Unlock()
	}
}

// Enable starts a previously registered but disabled plugin.
func (m *Manager) Enable(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.plugins[name]
	if !ok {
		return fmt.Errorf("plugin %q not registered", name)
	}
	inst.Config.Enabled = true
	if inst.Config.Daemon {
		if err := m.startPlugin(ctx, inst); err != nil {
			return fmt.Errorf("start plugin %q: %w", name, err)
		}
	}
	return nil
}

// Disable stops a running plugin and marks it disabled.
func (m *Manager) Disable(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.plugins[name]
	if !ok {
		return fmt.Errorf("plugin %q not registered", name)
	}
	inst.Config.Enabled = false
	inst.mu.Lock()
	if inst.proc != nil {
		m.log.Info("plugin: disabling", "plugin", name)
		signalTerm(inst.proc)
		proc := inst.proc
		inst.mu.Unlock()
		done := make(chan struct{})
		go func() {
			_, _ = proc.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			signalKill(proc)
			<-done
		}
		inst.mu.Lock()
		inst.proc = nil
		inst.mu.Unlock()
	} else {
		inst.mu.Unlock()
	}
	return nil
}

// Plugins returns a snapshot of all registered plugins with their status.
func (m *Manager) Plugins() []PluginStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []PluginStatus
	for name, inst := range m.plugins {
		inst.mu.Lock()
		status := PluginStatus{
			Name:    name,
			Enabled: inst.Config.Enabled,
			Running: inst.proc != nil,
			Healthy: inst.healthy,
			Daemon:  inst.Config.Daemon,
		}
		if inst.proc != nil {
			status.PID = inst.proc.Pid
		}
		inst.mu.Unlock()
		out = append(out, status)
	}
	return out
}

// PluginStatus is the runtime state of a plugin.
type PluginStatus struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
	Healthy bool   `json:"healthy"`
	Daemon  bool   `json:"daemon"`
	PID     int    `json:"pid,omitempty"`
}

func (m *Manager) startPlugin(ctx context.Context, inst *Instance) error {
	cfg := inst.Config
	bin := cfg.Binary
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("binary %q not found in PATH", bin)
	}

	args := []string{
		"--sigil-ingest-url", m.ingestURL,
	}
	if cfg.PollInterval != "" {
		args = append(args, "--poll-interval", cfg.PollInterval)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = os.Stderr // route plugin output to daemon stderr
	cmd.Stderr = os.Stderr
	setProcGroup(cmd)

	// Pass plugin-specific env vars.
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}

	inst.mu.Lock()
	inst.proc = cmd.Process
	inst.healthy = true
	inst.mu.Unlock()

	// Reap in background.
	go func() {
		_ = cmd.Wait()
		inst.mu.Lock()
		inst.proc = nil
		inst.healthy = false
		inst.mu.Unlock()
		m.log.Warn("plugin: process exited", "plugin", cfg.Name)
	}()

	return nil
}

func (m *Manager) healthLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.RLock()
			for _, inst := range m.plugins {
				if !inst.Config.Enabled || inst.Config.HealthURL == "" {
					continue
				}
				inst.mu.Lock()
				if inst.proc == nil {
					inst.healthy = false
					inst.mu.Unlock()
					continue
				}
				inst.mu.Unlock()
				// TODO: HTTP health check to inst.Config.HealthURL
			}
			m.mu.RUnlock()
		}
	}
}

// ParseEvent unmarshals a plugin event from JSON bytes.
func ParseEvent(data []byte) (*Event, error) {
	var e Event
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("plugin: parse event: %w", err)
	}
	if e.Plugin == "" {
		return nil, fmt.Errorf("plugin: event missing 'plugin' field")
	}
	if e.Kind == "" {
		return nil, fmt.Errorf("plugin: event missing 'kind' field")
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	return &e, nil
}
