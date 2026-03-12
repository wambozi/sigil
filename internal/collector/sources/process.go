package sources

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
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
	LastState string // last-seen /proc state letter: "R", "S", "Z", etc.
	ExitCode  int    // exit code captured from /proc status (-1 = unknown)
}

// ProcessSource polls /proc on a fixed interval and emits an event whenever a
// new interesting process appears (build tools, compilers, test runners, git,
// AI coding assistants).  When a tracked process exits, it emits a second event
// with the session duration so the analyzer can correlate AI tool usage with
// file activity.
type ProcessSource struct {
	// Interval is how often /proc is scanned.  Default: 5 seconds.
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

// pollExitState reads /proc/<pid>/status for every tracked session and caches
// the process state and exit code.  This captures the exit state before the
// PID vanishes from /proc entirely.
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

// readProcExitState reads /proc/<pid>/status and extracts the process state
// letter and, if available, the exit code.  Returns ("", -1) on any read error.
//
// Relevant lines in /proc/<pid>/status:
//
//	State:  S (sleeping)
//	State:  Z (zombie)
//
// The exit code is not directly in /proc/<pid>/status; instead we read
// /proc/<pid>/stat and parse field 52 (exit_code) which is valid for zombies.
func readProcExitState(pid string) (state string, exitCode int) {
	exitCode = -1

	// Read state from /proc/<pid>/status (more reliable parsing than /proc/<pid>/stat
	// for the state letter since comm can contain spaces/parens).
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
			// Format: "State:\tZ (zombie)" — extract the letter.
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				state = fields[1]
			}
			break
		}
	}

	// For zombie processes, try to read the exit code from /proc/<pid>/stat.
	// Field 52 (0-indexed) is exit_code, but we need to handle the comm field
	// (field 2) which is in parens and may contain spaces.
	if state == "Z" {
		exitCode = readExitCodeFromStat(pid)
	}

	return state, exitCode
}

// readExitCodeFromStat reads /proc/<pid>/stat and extracts the exit_code field
// (field index 51, 0-based, after parsing past the comm field in parens).
// Returns -1 if the file can't be read or parsed.
func readExitCodeFromStat(pid string) int {
	statPath := filepath.Join("/proc", pid, "stat")
	data, err := os.ReadFile(statPath)
	if err != nil {
		return -1
	}

	// /proc/<pid>/stat format: "pid (comm) state field3 field4 ..."
	// comm can contain spaces and parens, so find the last ')' to skip past it.
	content := string(data)
	closeParen := strings.LastIndex(content, ")")
	if closeParen < 0 || closeParen+2 >= len(content) {
		return -1
	}

	// Fields after comm start at index 0 = state (field 3 in proc(5)).
	// exit_code is field 52 in proc(5), which is index 49 after comm.
	fields := strings.Fields(content[closeParen+2:])
	if len(fields) < 50 {
		return -1
	}

	code, err := strconv.Atoi(fields[49])
	if err != nil {
		return -1
	}
	// The kernel stores exit_code as (exit_status << 8) | signal_number.
	return code >> 8
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
			"duration_sec":  int(duration.Seconds()),
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

// categorize returns a workflow category for a process based on its cmdline.
// Categories give the analyzer structured signal without needing to parse
// raw cmdlines.
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
// Only the executable/subcommand name is matched — never arguments or
// conversation content.

// AI coding assistants and LLM tools.
var aiKeywords = []string{
	// Anthropic
	"claude",
	// GitHub Copilot
	"copilot",
	// Cursor (editor ships its own process)
	"cursor",
	// Aider (CLI pair-programming)
	"aider",
	// Continue.dev
	"continue",
	// Sourcegraph Cody
	"cody",
	// Tabnine
	"tabnine",
	// Amazon Q / CodeWhisperer
	"amazon-q", "codewhisperer",
	// Codeium / Windsurf
	"codeium", "windsurf",
	// Ollama (local LLM runner)
	"ollama",
	// llama.cpp server
	"llama-server", "llama-cli",
	// OpenAI CLI
	"openai",
	// LM Studio
	"lmstudio",
	// GPT Engineer / Smol developer
	"gpt-engineer",
}

// Build tools and compilers.
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

// Test runners and frameworks.
var testKeywords = []string{
	"go test",
	"cargo test",
	"pytest", "python -m pytest", "unittest",
	"jest", "vitest", "mocha", "cypress", "playwright",
	"npm test", "pnpm test", "yarn test", "bun test",
	"nix flake check",
}

// Deployment and infrastructure tools.
var deployKeywords = []string{
	"docker", "podman",
	"kubectl", "helm",
	"terraform", "pulumi", "cdktf",
	"ansible",
	"nixos-rebuild",
	"ssh",
	"scp", "rsync",
}

// Version control.
var vcsKeywords = []string{
	"git",
	"gh ", // GitHub CLI (trailing space avoids matching "ghost" etc.)
	"jj",  // Jujutsu
}

// Language runtimes and package managers (catch-all).
var runtimeKeywords = []string{
	"node ", "deno", "bun ",
	"python", "python3",
	"ruby", "irb",
	"npm install", "pnpm install", "yarn install",
	"pip install", "pip3 install",
	"go mod", "go get",
	"cargo add",
}
