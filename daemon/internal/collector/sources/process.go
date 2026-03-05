package sources

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wambozi/aether/internal/event"
)

// ProcessSource polls /proc on a fixed interval and emits an event whenever a
// new interesting process appears (build tools, compilers, test runners, git).
// It intentionally avoids emitting one event per PID per poll to keep noise low.
type ProcessSource struct {
	// Interval is how often /proc is scanned.  Default: 5 seconds.
	Interval time.Duration

	// Keywords that mark a process as "interesting" from a workflow perspective.
	Keywords []string

	seen map[string]struct{} // cmdline → first-seen guard
}

func (s *ProcessSource) Name() string { return "process" }

func (s *ProcessSource) Events(ctx context.Context) (<-chan event.Event, error) {
	if s.Interval == 0 {
		s.Interval = 5 * time.Second
	}
	if len(s.Keywords) == 0 {
		s.Keywords = defaultKeywords
	}
	s.seen = make(map[string]struct{})

	ch := make(chan event.Event, 32)

	go func() {
		defer close(ch)

		ticker := time.NewTicker(s.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				procs, err := scanProc(s.Keywords)
				if err != nil {
					continue
				}
				for _, p := range procs {
					key := p["cmdline"].(string)
					if _, already := s.seen[key]; already {
						continue
					}
					s.seen[key] = struct{}{}

					e := event.Event{
						Kind:      event.KindProcess,
						Source:    s.Name(),
						Payload:   p,
						Timestamp: time.Now(),
					}
					select {
					case ch <- e:
					case <-ctx.Done():
						return
					}
				}
				// Evict stale entries so we re-emit when the same command
				// runs again in a later session.
				s.evictGone(procs)
			}
		}
	}()

	return ch, nil
}

// evictGone removes cmdlines from s.seen that are no longer running.
func (s *ProcessSource) evictGone(current []map[string]any) {
	live := make(map[string]struct{}, len(current))
	for _, p := range current {
		live[p["cmdline"].(string)] = struct{}{}
	}
	for k := range s.seen {
		if _, ok := live[k]; !ok {
			delete(s.seen, k)
		}
	}
}

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
		// Only numeric directories correspond to PIDs.
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
	// /proc/[pid]/cmdline is NUL-delimited.
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

func matchesAny(s string, keywords []string) bool {
	lower := strings.ToLower(s)
	for _, k := range keywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// defaultKeywords covers the most common developer workflows.
var defaultKeywords = []string{
	"go build", "go test", "go run",
	"cargo", "rustc",
	"make", "cmake", "ninja",
	"npm", "pnpm", "yarn", "bun",
	"python", "pytest",
	"docker", "podman",
	"kubectl", "helm",
	"git",
}
