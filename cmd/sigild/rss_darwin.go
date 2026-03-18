//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// readRSSMB returns the current process RSS in megabytes by shelling out to ps.
func readRSSMB() (int64, error) {
	pid := os.Getpid()
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, fmt.Errorf("ps rss: %w", err)
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse ps rss: %w", err)
	}
	return kb / 1024, nil
}
