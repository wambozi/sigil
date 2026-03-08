package actuator

import (
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wambozi/sigil/internal/event"
)

// BuildSplitActuator watches the collector Broadcast channel for build/test
// command starts and emits split-pane and close-split actions.
// It is event-driven, not poll-driven — it runs as a goroutine reading from
// the broadcast channel and calling registry.Notify directly.
type BuildSplitActuator struct {
	log          *slog.Logger
	pendingBuild bool
}

// NewBuildSplitActuator creates a new BuildSplitActuator.
func NewBuildSplitActuator(log *slog.Logger) *BuildSplitActuator {
	return &BuildSplitActuator{log: log}
}

// RunEventLoop reads events from the broadcast channel and emits split-pane
// actions when a build/test command is detected.
// splitNotify is called with action + a type string for the socket payload.
func (b *BuildSplitActuator) RunEventLoop(broadcast <-chan event.Event, splitNotify func(action Action, typ string)) {
	for ev := range broadcast {
		if ev.Kind != event.KindTerminal {
			continue
		}
		cmd, _ := ev.Payload["cmd"].(string)
		if cmd == "" {
			continue
		}

		if isTestOrBuildCmd(cmd) {
			exitCode := exitCodeFromPayload(ev.Payload)
			if exitCode == -1 && !b.pendingBuild {
				// Build started (exit_code -1 indicates in-progress, but
				// typically terminal events come with a final exit code).
				// For simplicity, treat any build/test command as a start event.
				b.pendingBuild = true
				action := Action{
					ID:          "build-split-" + uuid.New().String()[:8],
					Description: "Build started — split pane to show output",
					ExpiresAt:   time.Now().Add(30 * time.Minute),
				}
				splitNotify(action, "split-pane")
			} else if b.pendingBuild {
				// Build completed.
				b.pendingBuild = false
				action := Action{
					ID:          "build-close-" + uuid.New().String()[:8],
					Description: "Build completed — closing split",
					ExpiresAt:   time.Now().Add(30 * time.Second),
				}
				splitNotify(action, "close-split")
			} else if !b.pendingBuild {
				// First time seeing a build command — treat as start.
				b.pendingBuild = true
				action := Action{
					ID:          "build-split-" + uuid.New().String()[:8],
					Description: "Build started — split pane to show output",
					ExpiresAt:   time.Now().Add(30 * time.Minute),
				}
				splitNotify(action, "split-pane")
			}
		}
	}
}

// isTestOrBuildCmd reports whether cmd looks like a test or build invocation.
func isTestOrBuildCmd(cmd string) bool {
	if cmd == "" {
		return false
	}
	prefixes := []string{
		"go test", "go build", "go vet",
		"make", "cargo test", "cargo build",
		"npm test", "npm run test", "npm run build",
		"pytest", "python -m pytest",
		"./gradlew", "mvn test", "mvn build",
	}
	lower := strings.ToLower(strings.TrimSpace(cmd))
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

func exitCodeFromPayload(payload map[string]any) int {
	switch v := payload["exit_code"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return -1
}
