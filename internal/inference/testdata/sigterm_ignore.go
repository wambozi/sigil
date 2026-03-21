//go:build ignore

package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// Ignore SIGTERM; only exit on SIGKILL.
	signal.Ignore(syscall.SIGTERM)
	// Sleep indefinitely.
	for {
		time.Sleep(time.Hour)
		_ = os.Stdout
	}
}
