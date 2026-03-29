//go:build windows

package sources

import (
	"encoding/csv"
	"fmt"
	"os/exec"
	"strings"
)

// scanProc uses `tasklist /FO CSV /NH` to find running processes whose command
// line contains one of the provided keywords.
func scanProc(keywords []string) ([]map[string]any, error) {
	out, err := exec.Command("tasklist", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return nil, fmt.Errorf("tasklist: %w", err)
	}

	reader := csv.NewReader(strings.NewReader(string(out)))
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse tasklist csv: %w", err)
	}

	var results []map[string]any
	for _, record := range records {
		if len(record) < 2 {
			continue
		}

		// tasklist CSV columns: "Image Name","PID","Session Name","Session#","Mem Usage"
		imageName := strings.TrimSpace(record[0])
		pid := strings.TrimSpace(record[1])

		if !matchesAny(imageName, keywords) {
			continue
		}

		results = append(results, map[string]any{
			"pid":     pid,
			"comm":    imageName,
			"cmdline": imageName,
		})
	}
	return results, nil
}

// readProcExitState is limited on Windows. We can check if a process still
// exists but cannot read its state letter like on Linux.
func readProcExitState(pid string) (state string, exitCode int) {
	// Use tasklist with /FI to check if the process exists.
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %s", pid), "/NH", "/FO", "CSV").Output()
	if err != nil {
		return "", -1
	}
	if strings.Contains(string(out), pid) {
		return "R", -1 // running
	}
	return "", -1
}

// readExitCodeFromStat is not available on Windows.
func readExitCodeFromStat(_ string) int {
	return -1
}
