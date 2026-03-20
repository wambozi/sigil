//go:build linux

package sources

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// scanProc reads /proc to find running processes whose cmdline contains one of
// the provided keywords.
func scanProc(keywords []string) ([]map[string]any, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	var results []map[string]any
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !isNumeric(entry.Name()) {
			continue
		}

		cmdline, err := readCmdline(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		if !matchesAny(cmdline, keywords) {
			continue
		}

		comm, _ := readComm(filepath.Join("/proc", entry.Name(), "comm"))
		results = append(results, map[string]any{
			"pid":     entry.Name(),
			"comm":    strings.TrimSpace(comm),
			"cmdline": cmdline,
		})
	}
	return results, nil
}

func readCmdline(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(data), "\x00", " "), nil
}

func readComm(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Scan()
	return s.Text(), s.Err()
}

// readProcExitState reads /proc/<pid>/status and extracts the process state
// letter and, if available, the exit code.  Returns ("", -1) on any read error.
func readProcExitState(pid string) (state string, exitCode int) {
	exitCode = -1

	statusPath := filepath.Join("/proc", pid, "status")
	f, err := os.Open(statusPath)
	if err != nil {
		return "", -1
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "State:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				state = fields[1]
			}
			break
		}
	}

	if state == "Z" {
		exitCode = readExitCodeFromStat(pid)
	}

	return state, exitCode
}

// readExitCodeFromStat reads /proc/<pid>/stat and extracts the exit_code field.
// Returns -1 if the file can't be read or parsed.
func readExitCodeFromStat(pid string) int {
	statPath := filepath.Join("/proc", pid, "stat")
	data, err := os.ReadFile(statPath)
	if err != nil {
		return -1
	}

	content := string(data)
	closeParen := strings.LastIndex(content, ")")
	if closeParen < 0 || closeParen+2 >= len(content) {
		return -1
	}

	fields := strings.Fields(content[closeParen+2:])
	if len(fields) < 50 {
		return -1
	}

	code, err := strconv.Atoi(fields[49])
	if err != nil {
		return -1
	}
	return code >> 8
}
