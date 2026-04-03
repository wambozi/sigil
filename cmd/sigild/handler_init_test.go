package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteInitConfig(t *testing.T) {
	t.Run("writes_valid_config", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "sigil", "config.toml")
		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		p := initPayload{
			WatchDirs:         []string{"~/code", "~/projects"},
			InferenceMode:     "localfirst",
			NotificationLevel: 2,
			LocalInference:    true,
			CloudEnabled:      false,
			FleetEnabled:      false,
		}

		err := writeInitConfig(p)
		require.NoError(t, err)

		data, err := os.ReadFile(cfgPath)
		require.NoError(t, err)

		content := string(data)
		assert.Contains(t, content, `watch_dirs`)
		assert.Contains(t, content, `"~/code"`)
		assert.Contains(t, content, `"~/projects"`)
		assert.Contains(t, content, `mode = "localfirst"`)
		assert.Contains(t, content, `level = 2`)
		assert.Contains(t, content, `enabled = true`, "local inference should be enabled")
	})

	t.Run("uses_default_watch_dir_when_empty", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		p := initPayload{
			NotificationLevel: 3,
		}

		err := writeInitConfig(p)
		require.NoError(t, err)

		cfgPath := filepath.Join(tmpDir, "sigil", "config.toml")
		data, err := os.ReadFile(cfgPath)
		require.NoError(t, err)

		content := string(data)
		assert.Contains(t, content, `watch_dirs`)
		assert.Contains(t, content, `level = 3`)
	})

	t.Run("cloud_config_written_when_enabled", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		p := initPayload{
			WatchDirs:     []string{"~/code"},
			CloudEnabled:  true,
			CloudProvider: "anthropic",
			FleetEnabled:  true,
		}

		err := writeInitConfig(p)
		require.NoError(t, err)

		cfgPath := filepath.Join(tmpDir, "sigil", "config.toml")
		data, err := os.ReadFile(cfgPath)
		require.NoError(t, err)

		content := string(data)
		assert.Contains(t, content, `provider = "anthropic"`)
		assert.Contains(t, content, `[fleet]`)
		assert.Contains(t, content, `enabled = true`)
	})

	t.Run("clamps_invalid_notification_level", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		p := initPayload{
			WatchDirs:         []string{"~/code"},
			NotificationLevel: 99,
		}

		err := writeInitConfig(p)
		require.NoError(t, err)

		cfgPath := filepath.Join(tmpDir, "sigil", "config.toml")
		data, err := os.ReadFile(cfgPath)
		require.NoError(t, err)

		assert.Contains(t, string(data), `level = 2`)
	})
}

func TestWriteInitConfig_ml_and_repos(t *testing.T) {
	t.Run("ml_enabled_with_repos", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", tmpDir)

		p := initPayload{
			WatchDirs:      []string{"~/code"},
			RepoDirs:       []string{"~/code/sigil", "~/code/trader"},
			LocalInference: true,
			MLEnabled:      true,
			MLMode:         "local",
		}

		err := writeInitConfig(p)
		require.NoError(t, err)

		cfgPath := filepath.Join(tmpDir, "sigil", "config.toml")
		data, err := os.ReadFile(cfgPath)
		require.NoError(t, err)

		content := string(data)
		assert.Contains(t, content, `"~/code/sigil"`)
		assert.Contains(t, content, `"~/code/trader"`)
		assert.Contains(t, content, `mode = "local"`)
		assert.Contains(t, content, `[ml.local]`)
		assert.Contains(t, content, `enabled = true`)
	})
}

func TestDefaultWatchDir(t *testing.T) {
	t.Parallel()

	t.Run("returns_existing_dir", func(t *testing.T) {
		t.Parallel()

		tmpHome := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmpHome, "projects"), 0o755))

		result := defaultWatchDir(tmpHome)
		assert.Equal(t, "~/projects", result)
	})

	t.Run("falls_back_to_code", func(t *testing.T) {
		t.Parallel()

		tmpHome := t.TempDir()
		result := defaultWatchDir(tmpHome)
		assert.Equal(t, "~/code", result)
	})
}
