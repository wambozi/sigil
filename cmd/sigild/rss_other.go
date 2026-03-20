//go:build !linux && !darwin

package main

import "fmt"

// readRSSMB is a stub on unsupported platforms.
func readRSSMB() (int64, error) {
	return 0, fmt.Errorf("RSS monitoring not supported on this platform")
}
