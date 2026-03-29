//go:build windows

package main

// currentUID returns a placeholder UID on Windows.
// Windows doesn't have Unix UIDs. The UID is only used for socket path
// construction on Linux, which has its own code path on Windows.
func currentUID() int {
	return 0
}
