//go:build darwin

package main

import (
	"log/slog"

	"github.com/wambozi/sigil/internal/collector"
	"github.com/wambozi/sigil/internal/collector/sources"
)

// addPlatformSources registers macOS-only collector sources.
func addPlatformSources(col *collector.Collector, log *slog.Logger) {
	col.Add(&sources.DarwinFocusSource{})
	col.Add(sources.NewAppStateSource(log))
}
