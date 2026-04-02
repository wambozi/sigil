//go:build !darwin && !linux && !windows

package main

import (
	"log/slog"

	"github.com/wambozi/sigil/internal/collector"
)

// addPlatformSources is a no-op stub for unsupported platforms.
func addPlatformSources(_ *collector.Collector, _ *slog.Logger) {}
