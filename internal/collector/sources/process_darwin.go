//go:build darwin

package sources

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// scanProc uses `ps` to find running processes whose command line contains one
// of the provided keywords.  This is the macOS equivalent of scanning /proc.
func scanProc(keywords []string) ([]map[string]any, error) {
	// -A: all processes, -o: custom output format.
	// pid= and comm= suppress headers; args= gives the full command line.
	out, err := exec.Command("ps", "-Ao", "pid=,comm=,args=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}

	var results []map[string]any
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Format: "  PID COMM ARGS..."
		// PID is right-justified, then comm (basename), then full args.
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		pid := fields[0]
		comm := fields[1]
		args := strings.Join(fields[2:], " ")

		if !matchesAny(args, keywords) {
			continue
		}

		results = append(results, map[string]any{
			"pid":     pid,
			"comm":    comm,
			"cmdline": args,
		})
	}
	return results, nil
}

// readProcExitState reads the process state via `ps`.  On macOS, exit codes
// are not available for running processes; launchd reaps zombies aggressively
// so zombie detection is rare.  Returns ("", -1) if the process is gone.
func readProcExitState(pid string) (state string, exitCode int) {
	exitCode = -1

	out, err := exec.Command("ps", "-p", pid, "-o", "state=").Output()
	if err != nil {
		return "", -1
	}

	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", -1
	}
	// macOS ps state is a string like "S", "R", "U", etc.  Take the first character.
	return string(s[0]), -1
}

// readExitCodeFromStat is a stub on macOS — /proc/<pid>/stat does not exist.
func readExitCodeFromStat(_ string) int {
	return -1
}

// pidStr converts a PID int to string (used by tests).
func pidStr(pid int) string {
	return strconv.Itoa(pid)
}
