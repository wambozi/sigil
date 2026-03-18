//go:build linux

package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// readRSSMB returns the current process RSS in megabytes by parsing
// /proc/self/status.  Returns an error if the file cannot be read or parsed.
func readRSSMB() (int64, error) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, fmt.Errorf("open /proc/self/status: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse VmRSS: %w", err)
		}
		return kb / 1024, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan /proc/self/status: %w", err)
	}
	return 0, fmt.Errorf("VmRSS not found in /proc/self/status")
}
