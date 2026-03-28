//go:build darwin

package main

import (
	"github.com/wambozi/sigil/internal/collector"
	"github.com/wambozi/sigil/internal/collector/sources"
)

// addPlatformSources registers macOS-specific collector sources.
func addPlatformSources(col *collector.Collector) {
	col.Add(&sources.DarwinFocusSource{})
	col.Add(&sources.ClipboardSource{})
}
