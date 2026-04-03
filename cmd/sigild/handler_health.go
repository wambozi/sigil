package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"time"

	"github.com/wambozi/sigil/internal/config"
	"github.com/wambozi/sigil/internal/socket"
)

// serviceHealth describes the health of a single backend service.
type serviceHealth struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`  // "ok", "degraded", "down", "disabled"
	Message string         `json:"message"` // user-friendly, no jargon
	Actions []healthAction `json:"actions"` // push-button fixes
}

// healthAction is a single fix the user can apply with one click.
type healthAction struct {
	Label  string `json:"label"`  // button text
	Action string `json:"action"` // machine-readable action code
}

// registerHealthHandler adds the health socket method.
func registerHealthHandler(srv *socket.Server, cfg daemonConfig) {
	srv.Handle("health", func(ctx context.Context, _ socket.Request) socket.Response {
		// Load config from disk to pick up changes from set-config.
		liveCfg, err := config.Load(cfg.configPath)
		if err != nil {
			liveCfg = cfg.fileCfg
		}
		live := cfg
		live.fileCfg = liveCfg

		services := []serviceHealth{
			checkLLMHealth(live),
			checkMLHealth(live),
		}

		payload, _ := json.Marshal(map[string]any{
			"services": services,
		})
		return socket.Response{OK: true, Payload: payload}
	})
}

func checkLLMHealth(cfg daemonConfig) serviceHealth {
	localEnabled := cfg.fileCfg.Inference.Local.Enabled
	cloudEnabled := cfg.fileCfg.Inference.Cloud.Enabled
	hasCloudCreds := cfg.fileCfg.Inference.Cloud.APIKey != "" || cfg.fileCfg.Cloud.APIKey != ""

	if !localEnabled && !cloudEnabled {
		return serviceHealth{
			Name:    "AI Suggestions",
			Status:  "disabled",
			Message: "AI is turned off — suggestions use heuristics only",
			Actions: []healthAction{
				{Label: "Enable Cloud AI", Action: "enable_cloud_llm"},
				{Label: "Set Up Local AI", Action: "enable_local_llm"},
			},
		}
	}

	if localEnabled {
		url := cfg.fileCfg.Inference.Local.ServerURL
		if url == "" {
			url = "http://127.0.0.1:11434"
		}
		if err := pingHTTP(url + "/health"); err == nil {
			return serviceHealth{
				Name:    "AI Suggestions",
				Status:  "ok",
				Message: "Running on your machine",
			}
		}

		// Local is down — check if binary exists.
		serverBin := cfg.fileCfg.Inference.Local.ServerBin
		if serverBin == "" {
			serverBin = "llama-server"
		}
		_, binErr := exec.LookPath(serverBin)
		hasBinary := binErr == nil

		// Local is down but cloud fallback is active.
		if cloudEnabled && hasCloudCreds {
			return serviceHealth{
				Name:    "AI Suggestions",
				Status:  "ok",
				Message: "Using cloud (local model is offline)",
				Actions: []healthAction{
					{Label: "Start Local Model", Action: "restart_daemon"},
				},
			}
		}
		if cloudEnabled && !hasCloudCreds {
			actions := []healthAction{
				{Label: "Sign In to Cloud", Action: "cloud_signin"},
			}
			if hasBinary {
				actions = append([]healthAction{
					{Label: "Start Local Model", Action: "restart_daemon"},
				}, actions...)
			}
			return serviceHealth{
				Name:    "AI Suggestions",
				Status:  "down",
				Message: "Local model is offline and cloud isn't signed in",
				Actions: actions,
			}
		}

		// Local only, and it's down.
		if hasBinary {
			return serviceHealth{
				Name:    "AI Suggestions",
				Status:  "down",
				Message: "Local model is offline",
				Actions: []healthAction{
					{Label: "Start Local Model", Action: "restart_daemon"},
					{Label: "Switch to Cloud AI", Action: "enable_cloud_llm"},
				},
			}
		}
		return serviceHealth{
			Name:    "AI Suggestions",
			Status:  "down",
			Message: "Local AI server is not installed",
			Actions: []healthAction{
				{Label: "Switch to Cloud AI", Action: "enable_cloud_llm"},
				{Label: "Turn Off AI", Action: "disable_llm"},
			},
		}
	}

	// Cloud only.
	if !hasCloudCreds {
		return serviceHealth{
			Name:    "AI Suggestions",
			Status:  "down",
			Message: "Cloud AI enabled but not signed in",
			Actions: []healthAction{
				{Label: "Sign In", Action: "cloud_signin"},
			},
		}
	}
	return serviceHealth{
		Name:    "AI Suggestions",
		Status:  "ok",
		Message: "Using cloud AI",
	}
}

func checkMLHealth(cfg daemonConfig) serviceHealth {
	mode := cfg.fileCfg.ML.Mode
	if mode == "disabled" || mode == "" {
		return serviceHealth{
			Name:    "Smart Predictions",
			Status:  "disabled",
			Message: "Predictions are turned off",
			Actions: []healthAction{
				{Label: "Enable Predictions", Action: "enable_ml"},
			},
		}
	}

	localEnabled := cfg.fileCfg.ML.Local.Enabled
	cloudEnabled := cfg.fileCfg.ML.Cloud.Enabled
	hasCloudCreds := cfg.fileCfg.ML.Cloud.APIKey != "" || cfg.fileCfg.Cloud.APIKey != ""

	if localEnabled {
		url := cfg.fileCfg.ML.Local.ServerURL
		if url == "" {
			url = "http://127.0.0.1:7774"
		}
		if err := pingHTTP(url + "/health"); err == nil {
			return serviceHealth{
				Name:    "Smart Predictions",
				Status:  "ok",
				Message: "Running on your machine",
			}
		}
		if cloudEnabled && hasCloudCreds {
			return serviceHealth{
				Name:    "Smart Predictions",
				Status:  "ok",
				Message: "Using cloud (local is offline)",
				Actions: []healthAction{
					{Label: "Restart Predictions", Action: "restart_daemon"},
				},
			}
		}

		serverBin := cfg.fileCfg.ML.Local.ServerBin
		if serverBin == "" {
			serverBin = "sigil-ml"
		}
		_, binErr := exec.LookPath(serverBin)

		if binErr == nil {
			return serviceHealth{
				Name:    "Smart Predictions",
				Status:  "down",
				Message: "Prediction service is offline",
				Actions: []healthAction{
					{Label: "Restart Predictions", Action: "restart_daemon"},
					{Label: "Switch to Cloud", Action: "enable_cloud_ml"},
				},
			}
		}
		return serviceHealth{
			Name:    "Smart Predictions",
			Status:  "down",
			Message: "Prediction service is not installed",
			Actions: []healthAction{
				{Label: "Switch to Cloud", Action: "enable_cloud_ml"},
				{Label: "Turn Off", Action: "disable_ml"},
			},
		}
	}

	if cloudEnabled {
		if !hasCloudCreds {
			return serviceHealth{
				Name:    "Smart Predictions",
				Status:  "down",
				Message: "Cloud predictions enabled but not signed in",
				Actions: []healthAction{
					{Label: "Sign In", Action: "cloud_signin"},
				},
			}
		}
		return serviceHealth{
			Name:    "Smart Predictions",
			Status:  "ok",
			Message: "Using cloud predictions",
		}
	}

	return serviceHealth{
		Name:    "Smart Predictions",
		Status:  "disabled",
		Message: "No prediction backend configured",
		Actions: []healthAction{
			{Label: "Enable Predictions", Action: "enable_ml"},
		},
	}
}

func pingHTTP(url string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
