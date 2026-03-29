//go:build !linux && !darwin && !windows

package sources

// scanProc is a no-op on unsupported platforms.
// The process source will emit no events but won't crash.
func scanProc(_ []string) ([]map[string]any, error) {
	return nil, nil
}

// readProcExitState is a no-op on unsupported platforms.
func readProcExitState(_ string) (string, int) {
	return "", -1
}

// readExitCodeFromStat is a no-op on unsupported platforms.
func readExitCodeFromStat(_ string) int {
	return -1
}
