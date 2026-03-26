//go:build !linux

package main

import (
	"log/slog"

	"github.com/wambozi/sigil/internal/collector"
)

func addPlatformSources(_ *collector.Collector, _ *slog.Logger) {
	// No platform-specific sources on this OS.
}
