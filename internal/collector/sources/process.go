// Package sources contains individual collector source implementations.
// Each source implements the collector.Source interface and emits events
// over a channel for as long as the provided context is live.
package sources

import (
	"context"
	"strings"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

// processSession tracks a running process from first-seen to exit.
type processSession struct {
	StartedAt time.Time
	Category  string // "ai", "build", "test", "deploy", "vcs", "runtime"
	Cmdline   string
	Comm      string
	PID       string
	LastState string // last-seen state letter: "R", "S", "Z", etc.
	ExitCode  int    // exit code captured from process status (-1 = unknown)
}

// ProcessSource polls the OS process table on a fixed interval and emits an
// event whenever a new interesting process appears (build tools, compilers,
// test runners, git, AI coding assistants).  When a tracked process exits, it
// emits a second event with the session duration so the analyzer can correlate
// AI tool usage with file activity.
//
// The platform-specific process scanning is implemented in process_linux.go
// and process_darwin.go via the scanProc and readProcExitState functions.
type ProcessSource struct {
	// Interval is how often the process table is scanned.  Default: 5 seconds.
	Interval time.Duration

	// Keywords that mark a process as "interesting" from a workflow perspective.
	Keywords []string

	sessions map[string]*processSession // cmdline → session
}

func (s *ProcessSource) Name() string { return "process" }

func (s *ProcessSource) Events(ctx context.Context) (<-chan event.Event, error) {
	if s.Interval == 0 {
		s.Interval = 5 * time.Second
	}
	if len(s.Keywords) == 0 {
		s.Keywords = allKeywords()
	}
	s.sessions = make(map[string]*processSession)

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

				// Detect new processes.
				for _, p := range procs {
					key := p["cmdline"].(string)
					if _, active := s.sessions[key]; active {
						continue
					}

					cat := categorize(key)
					sess := &processSession{
						StartedAt: time.Now(),
						Category:  cat,
						Cmdline:   key,
						Comm:      p["comm"].(string),
						PID:       p["pid"].(string),
						LastState: "R",
						ExitCode:  -1,
					}
					s.sessions[key] = sess

					e := event.Event{
						Kind:   event.KindProcess,
						Source: s.Name(),
						Payload: map[string]any{
							"pid":      sess.PID,
							"comm":     sess.Comm,
							"cmdline":  sess.Cmdline,
							"category": sess.Category,
							"phase":    "start",
						},
						Timestamp: sess.StartedAt,
					}
					select {
					case ch <- e:
					case <-ctx.Done():
						return
					}
				}

				// Update exit state for tracked processes still alive.
				s.pollExitState()

				// Detect exited processes and emit duration events.
				s.emitExited(ch, ctx, procs)
			}
		}
	}()

	return ch, nil
}

// pollExitState reads the process state for every tracked session and caches
// the state and exit code before the process vanishes.
func (s *ProcessSource) pollExitState() {
	for _, sess := range s.sessions {
		state, code := readProcExitState(sess.PID)
		if state != "" {
			sess.LastState = state
		}
		if code >= 0 {
			sess.ExitCode = code
		}
	}
}

// emitExited finds sessions whose processes are no longer running, emits a
// process-end event with duration, and removes them from tracking.
func (s *ProcessSource) emitExited(ch chan<- event.Event, ctx context.Context, current []map[string]any) {
	live := make(map[string]struct{}, len(current))
	for _, p := range current {
		live[p["cmdline"].(string)] = struct{}{}
	}

	for key, sess := range s.sessions {
		if _, ok := live[key]; ok {
			continue
		}

		duration := time.Since(sess.StartedAt)
		delete(s.sessions, key)

		// Only emit end events for sessions longer than 2 seconds.
		// Short-lived processes (git status, quick builds) are noise.
		if duration < 2*time.Second {
			continue
		}

		payload := map[string]any{
			"pid":          sess.PID,
			"comm":         sess.Comm,
			"cmdline":      sess.Cmdline,
			"category":     sess.Category,
			"phase":        "end",
			"duration_sec": int(duration.Seconds()),
			"exit_state":   sess.LastState,
		}
		if sess.ExitCode >= 0 {
			payload["exit_code"] = sess.ExitCode
		}

		e := event.Event{
			Kind:      event.KindProcess,
			Source:    s.Name(),
			Payload:   payload,
			Timestamp: time.Now(),
		}
		select {
		case ch <- e:
		case <-ctx.Done():
			return
		}
	}
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

// categorize returns a workflow category for a process based on its cmdline.
func categorize(cmdline string) string {
	lower := strings.ToLower(cmdline)
	for _, kw := range aiKeywords {
		if strings.Contains(lower, kw) {
			return "ai"
		}
	}
	for _, kw := range testKeywords {
		if strings.Contains(lower, kw) {
			return "test"
		}
	}
	for _, kw := range deployKeywords {
		if strings.Contains(lower, kw) {
			return "deploy"
		}
	}
	for _, kw := range vcsKeywords {
		if strings.Contains(lower, kw) {
			return "vcs"
		}
	}
	for _, kw := range buildKeywords {
		if strings.Contains(lower, kw) {
			return "build"
		}
	}
	return "runtime"
}

// allKeywords returns every keyword across all categories.
func allKeywords() []string {
	var all []string
	all = append(all, aiKeywords...)
	all = append(all, buildKeywords...)
	all = append(all, testKeywords...)
	all = append(all, deployKeywords...)
	all = append(all, vcsKeywords...)
	all = append(all, runtimeKeywords...)
	return all
}

// --- Keyword lists by category -----------------------------------------------

var aiKeywords = []string{
	"claude", "copilot", "cursor", "aider", "continue", "cody", "tabnine",
	"amazon-q", "codewhisperer", "codeium", "windsurf", "ollama",
	"llama-server", "llama-cli", "openai", "lmstudio", "gpt-engineer",
}

var buildKeywords = []string{
	"go build", "go run", "go install",
	"cargo build", "cargo run", "rustc",
	"make", "cmake", "ninja",
	"gcc", "g++", "clang",
	"javac", "mvn", "gradle",
	"npm run", "pnpm run", "yarn run", "bun run",
	"webpack", "vite", "esbuild", "rollup",
	"nix build", "nix-build",
	"bazel",
}

var testKeywords = []string{
	"go test",
	"cargo test",
	"pytest", "python -m pytest", "unittest",
	"jest", "vitest", "mocha", "cypress", "playwright",
	"npm test", "pnpm test", "yarn test", "bun test",
	"nix flake check",
}

var deployKeywords = []string{
	"docker", "podman",
	"kubectl", "helm",
	"terraform", "pulumi", "cdktf",
	"ansible",
	"nixos-rebuild",
	"ssh",
	"scp", "rsync",
}

var vcsKeywords = []string{
	"git",
	"gh ", // GitHub CLI (trailing space avoids matching "ghost" etc.)
	"jj",  // Jujutsu
}

var runtimeKeywords = []string{
	"node ", "deno", "bun ",
	"python", "python3",
	"ruby", "irb",
	"npm install", "pnpm install", "yarn install",
	"pip install", "pip3 install",
	"go mod", "go get",
	"cargo add",
}
