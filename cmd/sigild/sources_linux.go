//go:build linux

package main

import (
	"log/slog"

	"github.com/wambozi/sigil/internal/collector"
	"github.com/wambozi/sigil/internal/collector/sources"
)

func addPlatformSources(col *collector.Collector, log *slog.Logger) {
	if src := sources.NewLinuxFocusSource(log); src != nil {
		col.Add(src)
	}
}
