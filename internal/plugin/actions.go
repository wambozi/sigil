package plugin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// PluginAction describes an action a plugin can perform.
type PluginAction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Command     string `json:"command"` // command template with <placeholders>
}

// Capabilities describes what a plugin can do — both data collection and actions.
type Capabilities struct {
	Plugin      string         `json:"plugin"`
	Actions     []PluginAction `json:"actions"`
	DataSources []string       `json:"data_sources"`
}

// DiscoverCapabilities runs `<binary> capabilities` and parses the output.
func DiscoverCapabilities(binary string) (*Capabilities, error) {
	binPath, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("binary %q not in PATH", binary)
	}

	out, err := exec.Command(binPath, "capabilities").Output()
	if err != nil {
		return nil, fmt.Errorf("capabilities command failed: %w", err)
	}

	var caps Capabilities
	if err := json.Unmarshal(out, &caps); err != nil {
		return nil, fmt.Errorf("parse capabilities: %w", err)
	}
	return &caps, nil
}

// ExecuteAction runs a plugin action by name with the given parameters.
func (m *Manager) ExecuteAction(pluginName, actionName string, params map[string]string) (string, error) {
	m.mu.RLock()
	inst, exists := m.plugins[pluginName]
	m.mu.RUnlock()

	if !exists || !inst.Config.Enabled {
		return "", fmt.Errorf("plugin %q not found or disabled", pluginName)
	}

	// Discover capabilities to find the action.
	caps, err := DiscoverCapabilities(inst.Config.Binary)
	if err != nil {
		return "", fmt.Errorf("discover capabilities for %q: %w", pluginName, err)
	}

	for _, action := range caps.Actions {
		if action.Name != actionName {
			continue
		}

		// Build the command from the template.
		cmd := action.Command
		for k, v := range params {
			cmd = strings.ReplaceAll(cmd, "<"+k+">", v)
		}

		// Execute.
		parts := strings.Fields(cmd)
		if len(parts) == 0 {
			return "", fmt.Errorf("empty command for action %q", actionName)
		}

		out, err := exec.Command(parts[0], parts[1:]...).CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("action %q failed: %w", actionName, err)
		}
		return string(out), nil
	}

	return "", fmt.Errorf("action %q not found on plugin %q", actionName, pluginName)
}

// AvailableActions returns all actions across all enabled plugins.
func (m *Manager) AvailableActions(log *slog.Logger) []PluginAction {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var all []PluginAction
	for name, inst := range m.plugins {
		if !inst.Config.Enabled {
			continue
		}
		caps, err := DiscoverCapabilities(inst.Config.Binary)
		if err != nil {
			log.Debug("plugin: capabilities unavailable", "plugin", name, "err", err)
			continue
		}
		all = append(all, caps.Actions...)
	}
	return all
}
