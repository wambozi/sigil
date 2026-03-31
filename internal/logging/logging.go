// Package logging provides shared log configuration for all sigil binaries.
//
// All binaries (sigild, sigilctl, sigil-app) write structured logs to
// ~/.local/share/sigild/logs/<component>.log alongside console output.
// sigil-ml (Python) writes to the same directory via its own logging config.
package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// LogDir returns the shared log directory path.
func LogDir() string {
	if runtime.GOOS == "windows" {
		appdata := os.Getenv("LOCALAPPDATA")
		if appdata == "" {
			appdata = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		return filepath.Join(appdata, "sigil", "sigild", "logs")
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "sigild", "logs")
}

// New creates a logger that writes to both stderr and a log file.
//
// The log file is written to ~/.local/share/sigild/logs/<component>.log.
// The file is opened in append mode. Rotation is left to external tooling
// (logrotate) or the OS — keeping the implementation dependency-free.
//
// component should be one of: "sigild", "sigilctl", "sigil-app".
func New(component string, level string) *slog.Logger {
	lvl := parseLevel(level)
	opts := &slog.HandlerOptions{Level: lvl}

	dir := LogDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// Fall back to stderr-only if we can't create the log directory.
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}

	logPath := filepath.Join(dir, component+".log")

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}

	// Write to both stderr and the log file.
	multi := io.MultiWriter(os.Stderr, f)
	return slog.New(slog.NewTextHandler(multi, opts))
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
