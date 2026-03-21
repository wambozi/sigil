package actuator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/event"
)

// mockTerminalQuerier implements TerminalQuerier for testing.
type mockTerminalQuerier struct {
	events []event.Event
	err    error
}

func (m *mockTerminalQuerier) QueryTerminalEvents(_ context.Context, _ time.Time) ([]event.Event, error) {
	return m.events, m.err
}

func TestContainerWarmActuator_Name(t *testing.T) {
	a := NewContainerWarmActuator(nil, nil, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := a.Name(); got != "container_warm" {
		t.Errorf("Name() = %q; want %q", got, "container_warm")
	}
}

func TestContainerWarmActuator_disabled(t *testing.T) {
	a := NewContainerWarmActuator(nil, nil, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	actions, err := a.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if actions != nil {
		t.Errorf("expected nil actions when disabled; got %v", actions)
	}
}

func TestContainerWarmActuator_noEvents(t *testing.T) {
	q := &mockTerminalQuerier{events: []event.Event{}}
	a := NewContainerWarmActuator(q, nil, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	actions, err := a.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if actions != nil {
		t.Errorf("expected nil actions when no events; got %v", actions)
	}
}

func TestContainerWarmActuator_storeError(t *testing.T) {
	q := &mockTerminalQuerier{err: errors.New("db unavailable")}
	a := NewContainerWarmActuator(q, nil, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := a.Check(context.Background())
	if err == nil {
		t.Fatal("expected error when store returns error; got nil")
	}
	if !errors.Is(err, q.err) {
		t.Errorf("error chain does not wrap store error: %v", err)
	}
}

// TestContainerWarmActuator_timeWindowNotMet verifies that Check returns nil
// when typicalHour is valid but the current time is outside the 2-minute
// warning window.
func TestContainerWarmActuator_timeWindowNotMet(t *testing.T) {
	now := time.Now()

	// Choose typicalHour such that warnHour != now.Hour() almost always.
	// warnHour = (typicalHour - 1 + 24) % 24; we want warnHour != now.Hour(),
	// so pick typicalHour = (now.Hour() + 2) % 24.
	typicalHour := (now.Hour() + 2) % 24
	sessionStart := time.Date(now.Year(), now.Month(), now.Day(), typicalHour, 0, 0, 0, now.Location())

	q := &mockTerminalQuerier{
		events: []event.Event{
			{Timestamp: sessionStart},
		},
	}

	dir := t.TempDir()
	composeContent := "services:\n  web:\n    image: nginx\n"
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(composeContent), 0o644); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := NewContainerWarmActuator(q, []string{dir}, true, log)

	actions, err := a.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	warnHour := (typicalHour - 1 + 24) % 24
	if now.Hour() == warnHour && now.Minute() >= 58 {
		t.Skip("wall clock is in the warning window — skipping to avoid flakiness")
	}
	if actions != nil {
		t.Errorf("expected nil actions when time window not met; got %v", actions)
	}
}

// TestContainerWarmActuator_noComposeServices verifies Check returns nil when
// the time window is met but no docker-compose files are found.
func TestContainerWarmActuator_noComposeServices(t *testing.T) {
	now := time.Now()
	if now.Minute() < 58 {
		t.Skip("wall clock minute < 58 — cannot enter compose-check branch without time travel")
	}

	warnHour := now.Hour()
	typicalHour := (warnHour + 1) % 24
	sessionStart := time.Date(now.Year(), now.Month(), now.Day(), typicalHour, 0, 0, 0, now.Location())

	q := &mockTerminalQuerier{
		events: []event.Event{{Timestamp: sessionStart}},
	}

	emptyDir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := NewContainerWarmActuator(q, []string{emptyDir}, true, log)

	actions, err := a.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if actions != nil {
		t.Errorf("expected nil when no compose services found; got %v", actions)
	}
}

// TestContainerWarmActuator_Check_noStoppedContainers drives Check through the
// compose-services branch by placing the wall clock in the warning window.
// Only runs when the current minute happens to be >= 58.
func TestContainerWarmActuator_Check_noStoppedContainers(t *testing.T) {
	now := time.Now()
	if now.Minute() < 58 {
		t.Skip("wall clock minute < 58; cannot enter time-window branch without time travel")
	}

	warnHour := now.Hour()
	typicalHour := (warnHour + 1) % 24
	sessionStart := time.Date(now.Year(), now.Month(), now.Day(), typicalHour, 0, 0, 0, now.Location())

	q := &mockTerminalQuerier{
		events: []event.Event{{Timestamp: sessionStart}},
	}

	dir := t.TempDir()
	compose := "services:\n  testwebsvc:\n    image: nginx\n"
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := NewContainerWarmActuator(q, []string{dir}, true, log)
	actions, err := a.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Either nil (no stopped containers) or non-nil if docker happens to have
	// a stopped container named "testwebsvc". Both are valid outcomes.
	_ = actions
}

func TestFindTypicalStartHour(t *testing.T) {
	a := &ContainerWarmActuator{}

	t.Run("empty events returns -1", func(t *testing.T) {
		got := a.findTypicalStartHour(nil)
		if got != -1 {
			t.Errorf("want -1; got %d", got)
		}
	})

	t.Run("single event is a session start", func(t *testing.T) {
		events := []event.Event{
			{Timestamp: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)},
		}
		got := a.findTypicalStartHour(events)
		if got != 9 {
			t.Errorf("want 9; got %d", got)
		}
	})

	t.Run("gap under 2h is not a new session", func(t *testing.T) {
		base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
		events := []event.Event{
			{Timestamp: base},
			{Timestamp: base.Add(30 * time.Minute)},
			{Timestamp: base.Add(90 * time.Minute)},
		}
		// Only the first event counts as a session start (hour 9).
		got := a.findTypicalStartHour(events)
		if got != 9 {
			t.Errorf("want 9; got %d", got)
		}
	})

	t.Run("2h+ gap triggers new session start", func(t *testing.T) {
		// Three sessions at hour 9, one session at hour 14 — hour 9 should win.
		events := []event.Event{
			// Day 1: session at 09:00, then gap, session at 14:00
			{Timestamp: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)},
			{Timestamp: time.Date(2026, 1, 1, 14, 0, 0, 0, time.UTC)},
			// Day 2: session at 09:00
			{Timestamp: time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)},
			// Day 3: session at 09:00
			{Timestamp: time.Date(2026, 1, 3, 9, 0, 0, 0, time.UTC)},
		}
		got := a.findTypicalStartHour(events)
		if got != 9 {
			t.Errorf("want 9; got %d", got)
		}
	})
}

func TestFindComposeServices(t *testing.T) {
	dir := t.TempDir()

	content := `version: "3"
services:
  web:
    image: nginx
  db:
    image: postgres
other:
  key: value
`
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write docker-compose.yml: %v", err)
	}

	a := &ContainerWarmActuator{watchPaths: []string{dir}}
	services := a.findComposeServices()

	if len(services) != 2 {
		t.Fatalf("expected 2 services; got %d: %v", len(services), services)
	}

	found := make(map[string]bool)
	for _, s := range services {
		found[s] = true
	}
	for _, want := range []string{"web", "db"} {
		if !found[want] {
			t.Errorf("expected service %q in results; got %v", want, services)
		}
	}
}

func TestFindComposeServices_composeDotYml(t *testing.T) {
	dir := t.TempDir()
	content := "services:\n  api:\n    image: myapp\n  worker:\n    image: myworker\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &ContainerWarmActuator{watchPaths: []string{dir}}
	services := a.findComposeServices()

	if len(services) != 2 {
		t.Fatalf("expected 2 services from compose.yml; got %d: %v", len(services), services)
	}
}

func TestFindComposeServices_composeYaml(t *testing.T) {
	dir := t.TempDir()
	content := "services:\n  proxy:\n    image: nginx\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &ContainerWarmActuator{watchPaths: []string{dir}}
	services := a.findComposeServices()

	if len(services) != 1 || services[0] != "proxy" {
		t.Errorf("expected [proxy]; got %v", services)
	}
}

func TestFindComposeServices_deduplicated(t *testing.T) {
	// Two compose files in the same directory both defining "web" — should
	// only be listed once.
	dir := t.TempDir()
	content := "services:\n  web:\n    image: nginx\n"
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &ContainerWarmActuator{watchPaths: []string{dir}}
	services := a.findComposeServices()

	if len(services) != 1 {
		t.Errorf("expected deduplicated list of 1; got %v", services)
	}
}

func TestFindComposeServices_noServiceSection(t *testing.T) {
	dir := t.TempDir()
	content := "version: \"3\"\nother:\n  key: value\n"
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	a := &ContainerWarmActuator{watchPaths: []string{dir}}
	services := a.findComposeServices()

	if len(services) != 0 {
		t.Errorf("expected 0 services when no 'services:' section; got %v", services)
	}
}

func TestFindComposeServices_emptyWatchPaths(t *testing.T) {
	a := &ContainerWarmActuator{watchPaths: nil}
	services := a.findComposeServices()
	if services != nil {
		t.Errorf("expected nil services for empty watchPaths; got %v", services)
	}
}

func TestFindStoppedContainers_dockerAbsent(t *testing.T) {
	// On systems where docker is not installed, this exercises the
	// error-return path of exec.Command. Must not panic.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := NewContainerWarmActuator(nil, nil, true, log)
	stopped := a.findStoppedContainers([]string{"web", "db"})
	_ = stopped // nil is expected when docker is absent
}

func TestFindStoppedContainers_emptyServices(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := NewContainerWarmActuator(nil, nil, true, log)

	// Empty service list → no matching names → empty result regardless of docker output.
	stopped := a.findStoppedContainers(nil)
	if stopped != nil {
		t.Errorf("expected nil for empty service list; got %v", stopped)
	}
}

// TestFindStoppedContainers_withFakeDocker injects a fake "docker" binary into
// PATH so findStoppedContainers exercises its output-parsing logic without a
// real Docker installation.
func TestFindStoppedContainers_withFakeDocker(t *testing.T) {
	bin := t.TempDir()
	fakeDocker := filepath.Join(bin, "docker")
	script := "#!/bin/sh\nprintf 'proj-db\tExited (0) 2 hours ago\nproj-web\tUp 3 hours\nother\tExited (1) 1 min ago\n'\n"
	if err := os.WriteFile(fakeDocker, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", bin+":"+origPath)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := NewContainerWarmActuator(nil, nil, true, log)

	services := []string{"db", "web"}
	stopped := a.findStoppedContainers(services)

	// Only "proj-db" is exited and contains "db".
	// "proj-web" is Up, not Exited, so it should not appear.
	if len(stopped) != 1 {
		t.Fatalf("expected 1 stopped container; got %v", stopped)
	}
	if stopped[0] != "proj-db" {
		t.Errorf("expected proj-db; got %q", stopped[0])
	}
}

func TestFindStoppedContainers_withFakeDocker_noMatch(t *testing.T) {
	bin := t.TempDir()
	fakeDocker := filepath.Join(bin, "docker")
	// All containers are running — none are exited.
	script := "#!/bin/sh\nprintf 'proj-api\tUp 5 minutes\nproj-cache\tUp 2 hours\n'\n"
	if err := os.WriteFile(fakeDocker, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", bin+":"+origPath)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := NewContainerWarmActuator(nil, nil, true, log)

	stopped := a.findStoppedContainers([]string{"api", "cache"})
	if stopped != nil {
		t.Errorf("expected nil when all containers running; got %v", stopped)
	}
}

func TestFindStoppedContainers_withFakeDocker_malformedLines(t *testing.T) {
	bin := t.TempDir()
	fakeDocker := filepath.Join(bin, "docker")
	// Mix of good and malformed lines.
	script := "#!/bin/sh\nprintf 'malformed-no-tab\napp-db\tExited (0) 1h ago\n'\n"
	if err := os.WriteFile(fakeDocker, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", bin+":"+origPath)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := NewContainerWarmActuator(nil, nil, true, log)

	stopped := a.findStoppedContainers([]string{"db"})
	if len(stopped) != 1 || stopped[0] != "app-db" {
		t.Errorf("expected [app-db]; got %v", stopped)
	}
}

// TestFindStoppedContainers_parsing tests the container-output parsing
// algorithm in isolation using a local reimplementation of the scanner logic.
// This verifies correctness of the parsing rules without requiring docker.
func TestFindStoppedContainers_parsing(t *testing.T) {
	parse := func(output string, services []string) []string {
		var stopped []string
		for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) != 2 {
				continue
			}
			name := parts[0]
			status := parts[1]
			for _, svc := range services {
				if strings.Contains(name, svc) && strings.HasPrefix(strings.ToLower(status), "exited") {
					stopped = append(stopped, name)
					break
				}
			}
		}
		return stopped
	}

	tests := []struct {
		name     string
		output   string
		services []string
		want     []string
	}{
		{
			name:     "running container not returned",
			output:   "myapp-web\tUp 5 minutes\n",
			services: []string{"web"},
			want:     nil,
		},
		{
			name:     "exited container returned",
			output:   "myapp-db\tExited (0) 2 hours ago\n",
			services: []string{"db"},
			want:     []string{"myapp-db"},
		},
		{
			name:     "multiple containers, mixed status",
			output:   "proj-web\tUp 3 hours\nproj-db\tExited (1) 5 minutes ago\n",
			services: []string{"web", "db"},
			want:     []string{"proj-db"},
		},
		{
			name:     "case insensitive exited",
			output:   "proj-cache\texited (0) 1 hour ago\n",
			services: []string{"cache"},
			want:     []string{"proj-cache"},
		},
		{
			name:     "service not in output returns empty",
			output:   "other-container\tExited (0) 1 min ago\n",
			services: []string{"db"},
			want:     nil,
		},
		{
			name:     "malformed line skipped",
			output:   "no-tab-here\nexited-db\tExited (0) now\n",
			services: []string{"db"},
			want:     []string{"exited-db"},
		},
		{
			name:     "empty output",
			output:   "",
			services: []string{"web"},
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parse(tt.output, tt.services)
			if len(got) != len(tt.want) {
				t.Errorf("got %v; want %v", got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q; want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
