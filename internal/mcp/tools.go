package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/plugin"
	"github.com/wambozi/sigil/internal/store"
)

// StoreReader is the read interface consumed by the MCP tool implementations.
type StoreReader interface {
	QueryCurrentTask(ctx context.Context) (*store.TaskRecord, error)
	QueryTaskHistory(ctx context.Context, since time.Time, limit int) ([]store.TaskRecord, error)
	QueryTasksByDate(ctx context.Context, date time.Time) ([]store.TaskRecord, error)
	QueryLatestPrediction(ctx context.Context, model string) (*store.PredictionRecord, error)
	QueryPredictions(ctx context.Context, model string, since time.Time) ([]store.PredictionRecord, error)
	QuerySuggestions(ctx context.Context, status store.SuggestionStatus, n int) ([]store.Suggestion, error)
	QueryTopFiles(ctx context.Context, since time.Time, n int) ([]store.FileEditCount, error)
	QueryTerminalEvents(ctx context.Context, since time.Time) ([]event.Event, error)
	QueryPluginEvents(ctx context.Context, pluginName string, since time.Time, limit int) ([]store.PluginEventRecord, error)
}

// RegisterStoreTools registers the 10 standard MCP tools on the given registry.
func RegisterStoreTools(reg *Registry, s StoreReader) {
	reg.Register(Tool{
		Name:        "get_current_task",
		Description: "Returns the current active development task (phase, branch, files touched, etc.)",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(ctx context.Context, _ json.RawMessage) (string, error) {
			t, err := s.QueryCurrentTask(ctx)
			if err != nil {
				return "", err
			}
			if t == nil {
				return `{"task": null}`, nil
			}
			b, err := json.Marshal(t)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	reg.Register(Tool{
		Name:        "get_task_history",
		Description: "Returns recent development tasks. Optional 'limit' arg (default 5).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "description": "Max tasks to return (default 5)"},
			},
		},
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Limit int `json:"limit"`
			}
			_ = json.Unmarshal(args, &p)
			if p.Limit <= 0 {
				p.Limit = 5
			}
			since := time.Now().AddDate(0, 0, -7)
			tasks, err := s.QueryTaskHistory(ctx, since, p.Limit)
			if err != nil {
				return "", err
			}
			b, err := json.Marshal(tasks)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	reg.Register(Tool{
		Name:        "get_predictions",
		Description: "Returns latest ML predictions. Optional 'model' arg filters by model name; omit for all recent predictions.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"model": map[string]any{"type": "string", "description": "Model name to filter by"},
			},
		},
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Model string `json:"model"`
			}
			_ = json.Unmarshal(args, &p)

			if p.Model != "" {
				pred, err := s.QueryLatestPrediction(ctx, p.Model)
				if err != nil {
					return "", err
				}
				if pred == nil {
					return `{"predictions": []}`, nil
				}
				b, err := json.Marshal([]store.PredictionRecord{*pred})
				if err != nil {
					return "", err
				}
				return string(b), nil
			}

			// No specific model — return latest prediction for each known model.
			since := time.Now().Add(-24 * time.Hour)
			models := []string{"activity", "suggest", "quality", "stuck", "duration"}
			var all []store.PredictionRecord
			for _, m := range models {
				preds, err := s.QueryPredictions(ctx, m, since)
				if err != nil {
					continue
				}
				all = append(all, preds...)
			}
			b, err := json.Marshal(all)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	reg.Register(Tool{
		Name:        "get_quality_score",
		Description: "Returns the latest 'quality' ML prediction score and details.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(ctx context.Context, _ json.RawMessage) (string, error) {
			pred, err := s.QueryLatestPrediction(ctx, "quality")
			if err != nil {
				return "", err
			}
			if pred == nil {
				return `{"quality": null}`, nil
			}
			b, err := json.Marshal(pred)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	reg.Register(Tool{
		Name:        "get_suggestions",
		Description: "Returns recent suggestions. Optional 'limit' arg (default 10).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "description": "Max suggestions to return (default 10)"},
			},
		},
		Fn: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Limit int `json:"limit"`
			}
			_ = json.Unmarshal(args, &p)
			if p.Limit <= 0 {
				p.Limit = 10
			}
			sgs, err := s.QuerySuggestions(ctx, "", p.Limit)
			if err != nil {
				return "", err
			}
			b, err := json.Marshal(sgs)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	reg.Register(Tool{
		Name:        "get_top_files",
		Description: "Returns the top 10 most-edited files today.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(ctx context.Context, _ json.RawMessage) (string, error) {
			now := time.Now()
			startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			files, err := s.QueryTopFiles(ctx, startOfDay, 10)
			if err != nil {
				return "", err
			}
			b, err := json.Marshal(files)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	reg.Register(Tool{
		Name:        "get_pr_status",
		Description: "Returns recent GitHub PR status events from the github plugin.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(ctx context.Context, _ json.RawMessage) (string, error) {
			since := time.Now().Add(-24 * time.Hour)
			evts, err := s.QueryPluginEvents(ctx, "github", since, 20)
			if err != nil {
				return "", err
			}
			// Filter to pr_status kind.
			var prEvents []store.PluginEventRecord
			for _, e := range evts {
				if e.Kind == "pr_status" {
					prEvents = append(prEvents, e)
				}
			}
			b, err := json.Marshal(prEvents)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	reg.Register(Tool{
		Name:        "get_ci_status",
		Description: "Returns recent CI status events from the github plugin.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(ctx context.Context, _ json.RawMessage) (string, error) {
			since := time.Now().Add(-24 * time.Hour)
			evts, err := s.QueryPluginEvents(ctx, "github", since, 20)
			if err != nil {
				return "", err
			}
			// Filter to ci_status kind.
			var ciEvents []store.PluginEventRecord
			for _, e := range evts {
				if e.Kind == "ci_status" {
					ciEvents = append(ciEvents, e)
				}
			}
			b, err := json.Marshal(ciEvents)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	reg.Register(Tool{
		Name:        "get_recent_commands",
		Description: "Returns recent terminal command events from the last hour.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(ctx context.Context, _ json.RawMessage) (string, error) {
			since := time.Now().Add(-1 * time.Hour)
			evts, err := s.QueryTerminalEvents(ctx, since)
			if err != nil {
				return "", err
			}
			b, err := json.Marshal(evts)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	reg.Register(Tool{
		Name:        "get_day_summary",
		Description: "Returns all tasks for today, providing a summary of the day's work.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(ctx context.Context, _ json.RawMessage) (string, error) {
			tasks, err := s.QueryTasksByDate(ctx, time.Now())
			if err != nil {
				return "", err
			}
			b, err := json.Marshal(tasks)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	reg.Register(Tool{
		Name: "get_workflow_state",
		Description: "Returns the user's current workflow state including flow state " +
			"probabilities (deep_work, shallow_work, exploring, blocked, winding_down), " +
			"momentum, focus score, and activity breakdown. Use this to understand what " +
			"the user is doing and how it's going before generating suggestions.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(ctx context.Context, _ json.RawMessage) (string, error) {
			pred, err := s.QueryLatestPrediction(ctx, "suggest")
			if err != nil {
				return "", err
			}
			if pred == nil {
				return `{"workflow_state": null, "note": "No workflow state prediction available yet. The ML sidecar may not be running or has not produced predictions."}`, nil
			}
			b, err := json.Marshal(pred)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	reg.Register(Tool{
		Name: "get_activity_stream",
		Description: "Returns recent events classified by activity type (creating, " +
			"refining, verifying, navigating, researching, integrating, communicating, " +
			"idle). Shows what the user has been doing in the last few minutes.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(ctx context.Context, _ json.RawMessage) (string, error) {
			pred, err := s.QueryLatestPrediction(ctx, "activity")
			if err != nil {
				return "", err
			}
			if pred == nil {
				return `{"activity_stream": null, "note": "No activity classification available yet. The ML sidecar may not be running or has not produced classifications."}`, nil
			}
			b, err := json.Marshal(pred)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})
}

// RegisterPluginTools adds MCP tools that expose plugin metadata, capabilities,
// and available actions to the inference engine. Call this after the plugin
// manager has been created and plugins have been registered.
func RegisterPluginTools(reg *Registry, mgr *plugin.Manager, log *slog.Logger) {
	reg.Register(Tool{
		Name: "get_installed_plugins",
		Description: "Returns all installed plugins with their runtime status " +
			"(enabled, running, healthy) and capabilities (actions they can perform, " +
			"data sources they provide). Use this to understand what integrations " +
			"are available and what data the system can access.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(ctx context.Context, _ json.RawMessage) (string, error) {
			statuses := mgr.Plugins()

			type pluginInfo struct {
				Name        string                `json:"name"`
				Enabled     bool                  `json:"enabled"`
				Running     bool                  `json:"running"`
				Healthy     bool                  `json:"healthy"`
				Actions     []plugin.PluginAction `json:"actions,omitempty"`
				DataSources []string              `json:"data_sources,omitempty"`
			}

			var result []pluginInfo
			for _, s := range statuses {
				info := pluginInfo{
					Name:    s.Name,
					Enabled: s.Enabled,
					Running: s.Running,
					Healthy: s.Healthy,
				}

				if s.Enabled {
					// Try to discover capabilities for running plugins.
					caps, err := plugin.DiscoverCapabilities(s.Name)
					if err == nil && caps != nil {
						info.Actions = caps.Actions
						info.DataSources = caps.DataSources
					}
				}

				result = append(result, info)
			}

			b, err := json.Marshal(result)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})

	reg.Register(Tool{
		Name: "get_available_actions",
		Description: "Returns all actions available across installed plugins. " +
			"Each action has a name, description, and command template. " +
			"Use this to understand what automated actions can be performed.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(ctx context.Context, _ json.RawMessage) (string, error) {
			actions := mgr.AvailableActions(log)

			if len(actions) == 0 {
				return `{"actions": [], "note": "No plugin actions available. Plugins may not be installed or may not support the capabilities command."}`, nil
			}

			b, err := json.Marshal(map[string]any{"actions": actions})
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	})
}
