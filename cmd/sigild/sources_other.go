//go:build !darwin

package main

import (
	"log/slog"

	"github.com/wambozi/sigil/internal/collector"
)

// addPlatformSources is a no-op on non-macOS platforms.
func addPlatformSources(_ *collector.Collector, _ *slog.Logger) {}
