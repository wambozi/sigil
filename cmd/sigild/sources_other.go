//go:build !darwin

package main

import (
	"github.com/wambozi/sigil/internal/collector"
	"github.com/wambozi/sigil/internal/collector/sources"
)

// addPlatformSources registers Linux-specific collector sources.
// HyprlandSource (registered unconditionally) handles Linux window focus.
func addPlatformSources(col *collector.Collector) {
	col.Add(&sources.ClipboardSource{})
}
