//go:build !darwin && !linux && !windows

package main

import "fmt"

func installAppAutoStart(_ string) error {
	fmt.Println("  [skip] tray app auto-start not supported on this platform")
	return nil
}
