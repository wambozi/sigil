package main

import (
	"testing"

	"github.com/wambozi/sigil/internal/config"
)

func boolPtr(v bool) *bool { return &v }

func TestApplyTierDefaults(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *config.Config
		check func(t *testing.T, cfg *config.Config)
	}{
		{
			name: "free tier no changes",
			cfg: &config.Config{
				Cloud: config.CloudConfig{Tier: "free"},
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.Inference.Mode != "" {
					t.Errorf("Inference.Mode = %q; want empty", cfg.Inference.Mode)
				}
				if cfg.CloudSync.Enabled != nil {
					t.Errorf("CloudSync.Enabled = %v; want nil", *cfg.CloudSync.Enabled)
				}
			},
		},
		{
			name: "empty tier no changes",
			cfg:  &config.Config{},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.Inference.Mode != "" {
					t.Errorf("Inference.Mode = %q; want empty", cfg.Inference.Mode)
				}
				if cfg.CloudSync.Enabled != nil {
					t.Errorf("CloudSync.Enabled = %v; want nil", *cfg.CloudSync.Enabled)
				}
			},
		},
		{
			name: "pro tier sets mode to remotefirst when empty",
			cfg: &config.Config{
				Cloud: config.CloudConfig{Tier: "pro"},
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.Inference.Mode != "remotefirst" {
					t.Errorf("Inference.Mode = %q; want remotefirst", cfg.Inference.Mode)
				}
			},
		},
		{
			name: "pro tier preserves explicit mode",
			cfg: &config.Config{
				Cloud:     config.CloudConfig{Tier: "pro"},
				Inference: config.InferenceConfig{Mode: "local"},
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.Inference.Mode != "local" {
					t.Errorf("Inference.Mode = %q; want local (preserved)", cfg.Inference.Mode)
				}
			},
		},
		{
			name: "team tier sets mode to remotefirst and sync enabled when nil",
			cfg: &config.Config{
				Cloud: config.CloudConfig{Tier: "team"},
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.Inference.Mode != "remotefirst" {
					t.Errorf("Inference.Mode = %q; want remotefirst", cfg.Inference.Mode)
				}
				if cfg.CloudSync.Enabled == nil || !*cfg.CloudSync.Enabled {
					t.Error("CloudSync.Enabled should be true")
				}
			},
		},
		{
			name: "team tier preserves explicit mode and explicit sync false",
			cfg: &config.Config{
				Cloud:     config.CloudConfig{Tier: "team"},
				Inference: config.InferenceConfig{Mode: "local"},
				CloudSync: config.CloudSyncConfig{Enabled: boolPtr(false)},
			},
			check: func(t *testing.T, cfg *config.Config) {
				if cfg.Inference.Mode != "local" {
					t.Errorf("Inference.Mode = %q; want local (preserved)", cfg.Inference.Mode)
				}
				if cfg.CloudSync.Enabled == nil || *cfg.CloudSync.Enabled {
					t.Error("CloudSync.Enabled should be false (preserved)")
				}
			},
		},
		{
			name: "unknown tier logs warning and does not panic",
			cfg: &config.Config{
				Cloud: config.CloudConfig{Tier: "enterprise"},
			},
			check: func(t *testing.T, cfg *config.Config) {
				// Just verify no panic and no defaults are set.
				if cfg.Inference.Mode != "" {
					t.Errorf("Inference.Mode = %q; want empty", cfg.Inference.Mode)
				}
				if cfg.CloudSync.Enabled != nil {
					t.Errorf("CloudSync.Enabled = %v; want nil", *cfg.CloudSync.Enabled)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			applyTierDefaults(tc.cfg)
			tc.check(t, tc.cfg)
		})
	}
}
