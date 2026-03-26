//go:build windows

package main

import (
	"log/slog"

	"github.com/wambozi/sigil/internal/collector"
	"github.com/wambozi/sigil/internal/collector/sources"
)

func addPlatformSources(col *collector.Collector, log *slog.Logger) {
	col.Add(sources.NewWindowsFocusSource(log))
	col.Add(sources.NewWindowsClipboardSource(log))
	col.Add(sources.NewWindowsAppStateSource(log))
}
