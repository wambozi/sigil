package actuator

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

// TerminalQuerier is the subset of the store needed to query terminal events.
type TerminalQuerier interface {
	QueryTerminalEvents(ctx context.Context, since time.Time) ([]event.Event, error)
}

// ContainerWarmActuator pre-warms idle Docker containers 2 minutes before the
// user's typical session start time.
type ContainerWarmActuator struct {
	store      TerminalQuerier
	log        *slog.Logger
	watchPaths []string
	enabled    bool
}

// NewContainerWarmActuator creates a new ContainerWarmActuator.
func NewContainerWarmActuator(store TerminalQuerier, watchPaths []string, enabled bool, log *slog.Logger) *ContainerWarmActuator {
	return &ContainerWarmActuator{
		store:      store,
		log:        log,
		watchPaths: watchPaths,
		enabled:    enabled,
	}
}

func (c *ContainerWarmActuator) Name() string { return "container_warm" }

func (c *ContainerWarmActuator) Check(ctx context.Context) ([]Action, error) {
	if !c.enabled {
		return nil, nil
	}

	// Determine the typical session start hour from the last 7 days.
	events, err := c.store.QueryTerminalEvents(ctx, time.Now().Add(-7*24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("container_warm: query events: %w", err)
	}
	if len(events) == 0 {
		return nil, nil
	}

	typicalHour := c.findTypicalStartHour(events)
	if typicalHour < 0 {
		return nil, nil
	}

	// Check if we're in the 2-minute warning window (1 hour before, minute >= 58).
	now := time.Now()
	warnHour := (typicalHour - 1 + 24) % 24
	if now.Hour() != warnHour || now.Minute() < 58 {
		return nil, nil
	}

	// Scan watchPaths for docker-compose.yml files.
	services := c.findComposeServices()
	if len(services) == 0 {
		return nil, nil
	}

	// Check for stopped containers matching compose services.
	stopped := c.findStoppedContainers(services)
	if len(stopped) == 0 {
		return nil, nil
	}

	var actions []Action
	for _, name := range stopped {
		actions = append(actions, Action{
			ID:          "container-warm-" + name,
			Description: "Pre-warming container: " + name,
			ExecuteCmd:  "docker start " + name,
			UndoCmd:     "docker stop " + name,
			ExpiresAt:   time.Now().Add(30 * time.Second),
		})
	}

	return actions, nil
}

// findTypicalStartHour determines the most common session start hour from
// terminal events. A session start is the first event after a 2+ hour gap.
func (c *ContainerWarmActuator) findTypicalStartHour(events []event.Event) int {
	if len(events) == 0 {
		return -1
	}

	startHours := make(map[int]int)
	// First event is always a session start.
	startHours[events[0].Timestamp.Hour()]++

	for i := 1; i < len(events); i++ {
		gap := events[i].Timestamp.Sub(events[i-1].Timestamp)
		if gap >= 2*time.Hour {
			startHours[events[i].Timestamp.Hour()]++
		}
	}

	bestHour := -1
	bestCount := 0
	for hour, count := range startHours {
		if count > bestCount {
			bestCount = count
			bestHour = hour
		}
	}
	return bestHour
}

// findComposeServices scans watchPaths for docker-compose.yml files and
// extracts service names from the "services:" section.
func (c *ContainerWarmActuator) findComposeServices() []string {
	var services []string
	seen := make(map[string]bool)

	for _, dir := range c.watchPaths {
		for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
			path := filepath.Join(dir, name)
			f, err := os.Open(path)
			if err != nil {
				continue
			}
			// Simple line-based parser: look for lines under "services:" that are
			// indented with 2 spaces and end with ":"
			scanner := bufio.NewScanner(f)
			inServices := false
			for scanner.Scan() {
				line := scanner.Text()
				if strings.TrimSpace(line) == "services:" {
					inServices = true
					continue
				}
				if inServices {
					// Service names are indented (typically 2 spaces) and end with ":"
					if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
						inServices = false
						continue
					}
					trimmed := strings.TrimSpace(line)
					if strings.HasSuffix(trimmed, ":") && !strings.Contains(trimmed, " ") {
						svc := strings.TrimSuffix(trimmed, ":")
						if !seen[svc] {
							seen[svc] = true
							services = append(services, svc)
						}
					}
				}
			}
			f.Close()
		}
	}
	return services
}

// findStoppedContainers runs `docker ps -a` and returns container names that
// match one of the given service names and are currently stopped.
func (c *ContainerWarmActuator) findStoppedContainers(services []string) []string {
	out, err := exec.Command("docker", "ps", "-a", "--format", "{{.Names}}\t{{.Status}}").Output()
	if err != nil {
		c.log.Debug("container_warm: docker ps failed", "err", err)
		return nil
	}

	serviceSet := make(map[string]bool)
	for _, s := range services {
		serviceSet[s] = true
	}

	var stopped []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 2)
		if len(parts) != 2 {
			continue
		}
		name := parts[0]
		status := parts[1]
		// Match service names that appear as substrings in container names
		for _, svc := range services {
			if strings.Contains(name, svc) && strings.HasPrefix(strings.ToLower(status), "exited") {
				stopped = append(stopped, name)
				break
			}
		}
	}
	return stopped
}
