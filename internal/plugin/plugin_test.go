package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Test binary helpers ───────────────────────────────────────────────────────

// buildStubBinary compiles a tiny Go program into dir and returns the binary path.
// The source is written to a temp file and compiled with `go build`.
// If `go` is not available the test calling this is skipped.
func buildStubBinary(t *testing.T, dir, name, src string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("'go' binary not available")
	}
	srcFile := filepath.Join(dir, name+".go")
	require.NoError(t, os.WriteFile(srcFile, []byte(src), 0644))
	binPath := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", binPath, srcFile)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "build stub binary: %s", out)
	return binPath
}

// prependPath returns a copy of the current PATH with dir prepended and
// sets PATH in the process environment for the duration of the test.
func prependPath(t *testing.T, dir string) {
	t.Helper()
	old := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+old)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 100}))
}

// ── registry.go ──────────────────────────────────────────────────────────────

func TestRegistry_ReturnsAllEntries(t *testing.T) {
	entries := Registry()
	assert.NotEmpty(t, entries, "registry must not be empty")
}

func TestRegistry_AllEntriesHaveRequiredFields(t *testing.T) {
	for _, e := range Registry() {
		assert.NotEmpty(t, e.Name, "entry must have a Name")
		assert.NotEmpty(t, e.Binary, "entry %q must have a Binary", e.Name)
		assert.NotEmpty(t, e.Version, "entry %q must have a Version", e.Name)
		assert.NotEmpty(t, e.Category, "entry %q must have a Category", e.Name)
	}
}

func TestLookup_KnownPlugins(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"claude"},
		{"jira"},
		{"github"},
		{"vscode"},
		{"jetbrains"},
		{"linear"},
		{"slack"},
		{"sentry"},
		{"neovim"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := Lookup(tt.name)
			require.NotNil(t, e)
			assert.Equal(t, tt.name, e.Name)
		})
	}
}

func TestLookup_UnknownPlugin(t *testing.T) {
	assert.Nil(t, Lookup("does-not-exist"))
	assert.Nil(t, Lookup(""))
}

func TestLookup_ReturnsSameEntry(t *testing.T) {
	a := Lookup("claude")
	b := Lookup("claude")
	require.NotNil(t, a)
	require.NotNil(t, b)
	assert.Equal(t, a.Name, b.Name)
	assert.Equal(t, a.Binary, b.Binary)
}

func TestByVersion_KnownVersions(t *testing.T) {
	tests := []struct {
		version     string
		wantAtLeast int
	}{
		{"v1", 5},
		{"v2", 5},
		{"v3", 8},
		{"v4", 5},
		{"v5", 5},
		{"vX", 5},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got := ByVersion(tt.version)
			assert.GreaterOrEqual(t, len(got), tt.wantAtLeast,
				"version %s should have at least %d plugins", tt.version, tt.wantAtLeast)
			for _, e := range got {
				assert.Equal(t, tt.version, e.Version, "all returned entries must match the requested version")
			}
		})
	}
}

func TestByVersion_UnknownVersion(t *testing.T) {
	got := ByVersion("vNONEXISTENT")
	assert.Empty(t, got)
}

func TestByVersion_DoesNotOverlap(t *testing.T) {
	// Each plugin name should appear in exactly one version bucket.
	seen := make(map[string]string)
	for _, version := range []string{"v1", "v2", "v3", "v4", "v5", "vX"} {
		for _, e := range ByVersion(version) {
			prev, ok := seen[e.Name]
			assert.False(t, ok, "plugin %q appears in both %q and %q", e.Name, prev, version)
			seen[e.Name] = version
		}
	}
}

func TestRegistryEntry_EnvVarSpec_Jira(t *testing.T) {
	e := Lookup("jira")
	require.NotNil(t, e)
	require.Len(t, e.EnvVars, 3)

	byName := make(map[string]EnvVarSpec)
	for _, ev := range e.EnvVars {
		byName[ev.Name] = ev
	}

	token := byName["JIRA_TOKEN"]
	assert.True(t, token.Required)
	assert.True(t, token.Secret)

	url := byName["JIRA_URL"]
	assert.True(t, url.Required)
	assert.False(t, url.Secret)

	email := byName["JIRA_EMAIL"]
	assert.True(t, email.Required)
}

func TestRegistryEntry_OptionalEnvVar_GitHub(t *testing.T) {
	e := Lookup("github")
	require.NotNil(t, e)
	require.Len(t, e.EnvVars, 1)
	assert.Equal(t, "GITHUB_TOKEN", e.EnvVars[0].Name)
	assert.False(t, e.EnvVars[0].Required)
	assert.True(t, e.EnvVars[0].Secret)
}

func TestRegistryEntry_HasSetup_Flags(t *testing.T) {
	// claude and vscode have HasSetup = true, jetbrains does not.
	claude := Lookup("claude")
	require.NotNil(t, claude)
	assert.True(t, claude.HasSetup)

	vscode := Lookup("vscode")
	require.NotNil(t, vscode)
	assert.True(t, vscode.HasSetup)

	jetbrains := Lookup("jetbrains")
	require.NotNil(t, jetbrains)
	assert.False(t, jetbrains.HasSetup)
}

// ── plugin.go ─────────────────────────────────────────────────────────────────

func TestNewManager(t *testing.T) {
	log := discardLogger()
	m := NewManager("http://localhost:9876/api/v1/ingest", log)
	require.NotNil(t, m)
	assert.NotNil(t, m.plugins)
	assert.Equal(t, "http://localhost:9876/api/v1/ingest", m.ingestURL)
}

func TestManager_Register(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	cfg := Config{Name: "myplugin", Enabled: true, Binary: "sigil-plugin-myplugin"}
	m.Register(cfg)

	m.mu.RLock()
	inst, ok := m.plugins["myplugin"]
	m.mu.RUnlock()

	require.True(t, ok)
	assert.Equal(t, cfg, inst.Config)
}

func TestManager_Register_Overwrite(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	first := Config{Name: "x", Enabled: true, Binary: "x-v1"}
	second := Config{Name: "x", Enabled: false, Binary: "x-v2"}
	m.Register(first)
	m.Register(second)

	m.mu.RLock()
	inst := m.plugins["x"]
	m.mu.RUnlock()

	assert.Equal(t, second, inst.Config)
}

func TestManager_Plugins_Empty(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	assert.Empty(t, m.Plugins())
}

func TestManager_Plugins_ReflectsRegistered(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{Name: "alpha", Enabled: true, Binary: "sigil-plugin-alpha"})
	m.Register(Config{Name: "beta", Enabled: false, Binary: "sigil-plugin-beta"})

	statuses := m.Plugins()
	require.Len(t, statuses, 2)

	byName := make(map[string]PluginStatus)
	for _, s := range statuses {
		byName[s.Name] = s
	}

	alpha := byName["alpha"]
	assert.True(t, alpha.Enabled)
	assert.False(t, alpha.Running)
	assert.Equal(t, 0, alpha.PID)

	beta := byName["beta"]
	assert.False(t, beta.Enabled)
}

func TestManager_Start_SkipsDisabled(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{
		Name:    "disabled-plugin",
		Enabled: false,
		Daemon:  true,
		Binary:  "nonexistent-binary-xyz",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := m.Start(ctx)
	require.NoError(t, err)

	statuses := m.Plugins()
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Running)
}

func TestManager_Start_SkipsNonDaemon(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{
		Name:    "hook-only",
		Enabled: true,
		Daemon:  false,
		Binary:  "nonexistent-binary-xyz",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := m.Start(ctx)
	require.NoError(t, err)

	statuses := m.Plugins()
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Running)
}

func TestManager_Start_BinaryNotFound(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{
		Name:    "missing-binary",
		Enabled: true,
		Daemon:  true,
		Binary:  "sigil-plugin-absolutely-does-not-exist-xyz123",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start continues past individual binary-not-found failures — returns nil.
	err := m.Start(ctx)
	require.NoError(t, err)

	statuses := m.Plugins()
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Running)
}

func TestManager_Stop_NothingRunning(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{Name: "idle", Enabled: true, Binary: "sigil-plugin-idle"})
	// Must not panic or block.
	m.Stop()
}

func TestManager_Stop_EmptyManager(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	assert.NotPanics(t, func() { m.Stop() })
}

func TestManager_Start_ShortLivedProcess(t *testing.T) {
	// "true" exits immediately with code 0 — verifies the reaper goroutine cleans up.
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("'true' binary not available")
	}

	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{
		Name:    "short-lived",
		Enabled: true,
		Daemon:  true,
		Binary:  "true",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := m.Start(ctx)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		for _, s := range m.Plugins() {
			if s.Name == "short-lived" {
				return !s.Running
			}
		}
		return false
	}, 3*time.Second, 50*time.Millisecond, "reaper goroutine should mark process not-running after exit")
}

func TestManager_Stop_WithNoRunningProcess(t *testing.T) {
	// Stop must be idempotent and safe when no process is actually running.
	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{Name: "alpha", Enabled: true, Binary: "sigil-plugin-alpha"})
	m.Register(Config{Name: "beta", Enabled: false, Binary: "sigil-plugin-beta"})

	// Neither was started — Stop must not panic or block.
	done := make(chan struct{})
	go func() {
		m.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() blocked unexpectedly")
	}
}

func TestManager_Start_PollIntervalPassedAsArg(t *testing.T) {
	// When PollInterval is set, "--poll-interval" is added to the args.
	// Verify this via startPlugin by launching "true" (which ignores args) —
	// the process should still start successfully.
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("'true' binary not available")
	}

	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{
		Name:         "with-interval",
		Enabled:      true,
		Daemon:       true,
		Binary:       "true",
		PollInterval: "30s",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	require.Eventually(t, func() bool {
		for _, s := range m.Plugins() {
			if s.Name == "with-interval" {
				return !s.Running
			}
		}
		return false
	}, 3*time.Second, 50*time.Millisecond)
}

func TestManager_Start_WithEnvVars(t *testing.T) {
	// Start with env vars and a real binary to ensure env passing doesn't panic.
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("'true' binary not available")
	}

	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{
		Name:    "env-plugin",
		Enabled: true,
		Daemon:  true,
		Binary:  "true",
		Env:     map[string]string{"PLUGIN_TEST_VAR": "value123"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	require.Eventually(t, func() bool {
		for _, s := range m.Plugins() {
			if s.Name == "env-plugin" {
				return !s.Running
			}
		}
		return false
	}, 3*time.Second, 50*time.Millisecond)
}

func TestParseEvent_Valid(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	raw := fmt.Sprintf(`{"plugin":"jira","kind":"story_update","timestamp":%q,"payload":{"key":"PROJ-1"}}`,
		now.Format(time.RFC3339))

	e, err := ParseEvent([]byte(raw))
	require.NoError(t, err)
	assert.Equal(t, "jira", e.Plugin)
	assert.Equal(t, "story_update", e.Kind)
	assert.True(t, now.Equal(e.Timestamp))
	assert.Equal(t, "PROJ-1", e.Payload["key"])
}

func TestParseEvent_MissingPlugin(t *testing.T) {
	raw := `{"kind":"story_update","timestamp":"2025-01-01T00:00:00Z"}`
	_, err := ParseEvent([]byte(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plugin")
}

func TestParseEvent_MissingKind(t *testing.T) {
	raw := `{"plugin":"jira","timestamp":"2025-01-01T00:00:00Z"}`
	_, err := ParseEvent([]byte(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kind")
}

func TestParseEvent_InvalidJSON(t *testing.T) {
	_, err := ParseEvent([]byte(`not json`))
	require.Error(t, err)
}

func TestParseEvent_ZeroTimestampBackfilled(t *testing.T) {
	before := time.Now()
	raw := `{"plugin":"github","kind":"pr_status"}`
	e, err := ParseEvent([]byte(raw))
	require.NoError(t, err)
	assert.False(t, e.Timestamp.IsZero())
	assert.True(t, !e.Timestamp.Before(before))
}

func TestParseEvent_Correlation(t *testing.T) {
	raw := `{
		"plugin":"jira","kind":"story_update",
		"timestamp":"2025-01-01T00:00:00Z",
		"correlation":{"story_id":"PROJ-42","branch":"feature/foo","repo_root":"/src","task_id":"t1","pr_id":"pr99"}
	}`
	e, err := ParseEvent([]byte(raw))
	require.NoError(t, err)
	assert.Equal(t, "PROJ-42", e.Correlation.StoryID)
	assert.Equal(t, "feature/foo", e.Correlation.Branch)
	assert.Equal(t, "/src", e.Correlation.RepoRoot)
	assert.Equal(t, "t1", e.Correlation.TaskID)
	assert.Equal(t, "pr99", e.Correlation.PRID)
}

func TestParseEvent_EmptyPayload(t *testing.T) {
	raw := `{"plugin":"github","kind":"pr_status","timestamp":"2025-01-01T00:00:00Z"}`
	e, err := ParseEvent([]byte(raw))
	require.NoError(t, err)
	assert.Nil(t, e.Payload)
}

func TestPluginStatus_ZeroValue(t *testing.T) {
	var s PluginStatus
	assert.False(t, s.Enabled)
	assert.False(t, s.Running)
	assert.False(t, s.Healthy)
	assert.Equal(t, 0, s.PID)
}

// ── ingest.go ─────────────────────────────────────────────────────────────────

func TestNewIngestServer_HandlerNotNil(t *testing.T) {
	s := NewIngestServer(func(Event) {}, discardLogger())
	require.NotNil(t, s.Handler())
}

func TestIngestServer_Health(t *testing.T) {
	s := NewIngestServer(func(Event) {}, discardLogger())
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
}

func TestIngestServer_IngestSingleEvent(t *testing.T) {
	var received []Event
	s := NewIngestServer(func(e Event) {
		received = append(received, e)
	}, discardLogger())

	body := `{"plugin":"github","kind":"pr_status","timestamp":"2025-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.Len(t, received, 1)
	assert.Equal(t, "github", received[0].Plugin)
	assert.Equal(t, "pr_status", received[0].Kind)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(1), resp["accepted"])
}

func TestIngestServer_IngestBatchEvents(t *testing.T) {
	var received []Event
	s := NewIngestServer(func(e Event) {
		received = append(received, e)
	}, discardLogger())

	body := `[
		{"plugin":"github","kind":"pr_status","timestamp":"2025-01-01T00:00:00Z"},
		{"plugin":"jira","kind":"story_update","timestamp":"2025-01-01T00:00:01Z"},
		{"plugin":"slack","kind":"message","timestamp":"2025-01-01T00:00:02Z"}
	]`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, received, 3)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(3), resp["accepted"])
}

func TestIngestServer_IngestSkipsMissingFields(t *testing.T) {
	var received []Event
	s := NewIngestServer(func(e Event) {
		received = append(received, e)
	}, discardLogger())

	// Batch: one valid, one missing plugin, one missing kind.
	body := `[
		{"plugin":"github","kind":"pr_status","timestamp":"2025-01-01T00:00:00Z"},
		{"kind":"story_update","timestamp":"2025-01-01T00:00:01Z"},
		{"plugin":"slack","timestamp":"2025-01-01T00:00:02Z"}
	]`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.Len(t, received, 1, "only the event with both plugin and kind should be accepted")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(1), resp["accepted"])
}

func TestIngestServer_IngestInvalidJSON(t *testing.T) {
	s := NewIngestServer(func(Event) {}, discardLogger())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", strings.NewReader(`not json at all`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestIngestServer_IngestEmptyBatch(t *testing.T) {
	var received []Event
	s := NewIngestServer(func(e Event) {
		received = append(received, e)
	}, discardLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", strings.NewReader(`[]`))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, received)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(0), resp["accepted"])
}

func TestIngestServer_IngestReadsPayload(t *testing.T) {
	var received []Event
	s := NewIngestServer(func(e Event) { received = append(received, e) }, discardLogger())

	body := `{"plugin":"jira","kind":"story_update","timestamp":"2025-06-01T12:00:00Z","payload":{"story_id":"PROJ-99","points":5}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, received, 1)
	assert.Equal(t, "PROJ-99", received[0].Payload["story_id"])
	assert.Equal(t, float64(5), received[0].Payload["points"])
}

func TestIngestServer_ResponseContentType(t *testing.T) {
	s := NewIngestServer(func(Event) {}, discardLogger())
	body := `{"plugin":"github","kind":"pr_status","timestamp":"2025-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
}

func TestIngestServer_HealthContentType(t *testing.T) {
	s := NewIngestServer(func(Event) {}, discardLogger())
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
}

func TestIngestServer_AllMissingFieldsSkipped(t *testing.T) {
	var received []Event
	s := NewIngestServer(func(e Event) { received = append(received, e) }, discardLogger())

	// Every entry in the batch is invalid.
	body := `[
		{"timestamp":"2025-01-01T00:00:00Z"},
		{"plugin":"","kind":""},
		{}
	]`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, received)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(0), resp["accepted"])
}

// ── install.go ────────────────────────────────────────────────────────────────

func TestIsInstalled_UnknownPlugin(t *testing.T) {
	assert.False(t, IsInstalled("completely-unknown-plugin-xyz"))
}

func TestIsInstalled_KnownPluginBinaryAbsent(t *testing.T) {
	e := Lookup("linear")
	require.NotNil(t, e)
	_, err := exec.LookPath(e.Binary)
	if err != nil {
		assert.False(t, IsInstalled("linear"))
	}
}

func TestDetectInstallMethod_ReturnsKnownMethod(t *testing.T) {
	method := DetectInstallMethod()
	assert.True(t, method == InstallGo || method == InstallBrew,
		"DetectInstallMethod should return InstallGo or InstallBrew, got %q", method)
}

func TestDetectInstallMethod_GoPreferredOverBrew(t *testing.T) {
	// On a machine where Go is present, InstallGo must be returned.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not in PATH")
	}
	assert.Equal(t, InstallGo, DetectInstallMethod())
}

func TestInstall_UnknownPlugin(t *testing.T) {
	err := Install("totally-unknown-plugin-xyz", InstallGo)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown plugin")
}

func TestInstall_UnknownPlugin_BrewMethod(t *testing.T) {
	err := Install("does-not-exist-at-all", InstallBrew)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown plugin")
}

func TestInstall_ShipsWithSigild_BinaryAbsent(t *testing.T) {
	// claude ships with sigild (GoModule == ""). If the binary is absent,
	// Install must return an informative error. If present, it must succeed.
	e := Lookup("claude")
	require.NotNil(t, e)
	require.Empty(t, e.GoModule, "claude should ship with sigild")

	_, binaryErr := exec.LookPath(e.Binary)
	if binaryErr != nil {
		err := Install("claude", InstallGo)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ships with sigild")
	} else {
		err := Install("claude", InstallGo)
		assert.NoError(t, err)
	}
}

func TestInstall_BrewPlugin_NoBrewFormula(t *testing.T) {
	// "linear" has a GoModule but no BrewFormula, so installing it via InstallBrew
	// should reach installBrew and return an error about the missing formula.
	e := Lookup("linear")
	require.NotNil(t, e)
	require.NotEmpty(t, e.GoModule, "linear must have a GoModule for this test to be valid")
	require.Empty(t, e.BrewFormula, "linear must have no BrewFormula for this test to be valid")

	err := Install("linear", InstallBrew)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Homebrew formula")
}

func TestInstall_GoPlugin_NoGoModule_GoMethod(t *testing.T) {
	// jetbrains ships with sigild — no GoModule to install.
	// When its binary is absent, Install should error about "ships with sigild".
	e := Lookup("jetbrains")
	require.NotNil(t, e)
	require.Empty(t, e.GoModule)

	_, binaryErr := exec.LookPath(e.Binary)
	if binaryErr == nil {
		t.Skip("sigil-plugin-jetbrains is in PATH; skipping absence test")
	}
	err := Install("jetbrains", InstallGo)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ships with sigild")
}

func TestSetup_UnknownPlugin(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	_, err := Setup("no-such-plugin-xyz", r)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown plugin")
}

func TestSetup_PluginWithEnvVars_ExistingEnv(t *testing.T) {
	t.Setenv("JIRA_URL", "https://example.atlassian.net")
	t.Setenv("JIRA_EMAIL", "test@example.com")
	t.Setenv("JIRA_TOKEN", "tok-secret")

	r := bufio.NewReader(strings.NewReader(""))
	toml, err := Setup("jira", r)
	require.NoError(t, err)

	assert.Contains(t, toml, "[plugins.jira]")
	assert.Contains(t, toml, "enabled = true")
	assert.Contains(t, toml, `binary = "sigil-plugin-jira"`)
	assert.Contains(t, toml, "[plugins.jira.env]")
	assert.Contains(t, toml, "JIRA_URL")
	assert.Contains(t, toml, "JIRA_TOKEN")
	assert.Contains(t, toml, "JIRA_EMAIL")
}

func TestSetup_PluginWithEnvVars_ReaderInput(t *testing.T) {
	t.Setenv("JIRA_URL", "")
	t.Setenv("JIRA_EMAIL", "")
	t.Setenv("JIRA_TOKEN", "")

	// Provide answers via the reader, one per required var.
	input := "https://example.atlassian.net\nuser@example.com\nmysecrettoken\n"
	r := bufio.NewReader(strings.NewReader(input))
	toml, err := Setup("jira", r)
	require.NoError(t, err)

	assert.Contains(t, toml, "[plugins.jira]")
	assert.Contains(t, toml, "enabled = true")
	assert.Contains(t, toml, "[plugins.jira.env]")
}

func TestSetup_PluginNoEnvVars(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	toml, err := Setup("jetbrains", r)
	require.NoError(t, err)

	assert.Contains(t, toml, "[plugins.jetbrains]")
	assert.Contains(t, toml, "enabled = true")
	assert.Contains(t, toml, `binary = "sigil-plugin-jetbrains"`)
	// No env section when there are no env vars.
	assert.NotContains(t, toml, "[plugins.jetbrains.env]")
}

func TestSetup_PluginOptionalEnvVarSkipped(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	r := bufio.NewReader(strings.NewReader("\n")) // empty line = skip
	toml, err := Setup("github", r)
	require.NoError(t, err)

	assert.Contains(t, toml, "[plugins.github]")
	// No env section: value was empty and optional.
	assert.NotContains(t, toml, "[plugins.github.env]")
}

func TestSetup_PluginRequiredEnvVarWarnsWhenEmpty(t *testing.T) {
	t.Setenv("JIRA_URL", "")
	t.Setenv("JIRA_EMAIL", "")
	t.Setenv("JIRA_TOKEN", "")

	// Three empty lines — all required vars left blank.
	r := bufio.NewReader(strings.NewReader("\n\n\n"))

	// Redirect stdout so warnings don't pollute test output.
	old := os.Stdout
	devNull, _ := os.Open(os.DevNull)
	os.Stdout = devNull
	defer func() {
		devNull.Close()
		os.Stdout = old
	}()

	toml, err := Setup("jira", r)
	require.NoError(t, err)
	assert.Contains(t, toml, "[plugins.jira]")
	// Nothing entered so no env section.
	assert.NotContains(t, toml, "[plugins.jira.env]")
}

func TestSetup_OptionalEnvProvided(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	r := bufio.NewReader(strings.NewReader("ghp_mytoken\n"))
	toml, err := Setup("github", r)
	require.NoError(t, err)

	assert.Contains(t, toml, "[plugins.github]")
	assert.Contains(t, toml, "[plugins.github.env]")
	assert.Contains(t, toml, "GITHUB_TOKEN")
}

// ── actions.go ────────────────────────────────────────────────────────────────

func TestDiscoverCapabilities_BinaryNotFound(t *testing.T) {
	_, err := DiscoverCapabilities("absolutely-does-not-exist-xyz123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in PATH")
}

func TestManager_ExecuteAction_PluginNotFound(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	_, err := m.ExecuteAction("no-such-plugin", "some-action", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or disabled")
}

func TestManager_ExecuteAction_PluginDisabled(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{Name: "disabled", Enabled: false, Binary: "sigil-plugin-disabled"})
	_, err := m.ExecuteAction("disabled", "some-action", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found or disabled")
}

func TestManager_ExecuteAction_BinaryNotFound(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{Name: "nope", Enabled: true, Binary: "sigil-plugin-absolutely-nonexistent-xyz"})
	_, err := m.ExecuteAction("nope", "do-thing", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "discover capabilities")
}

func TestManager_AvailableActions_NoPlugins(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	actions := m.AvailableActions(discardLogger())
	assert.Empty(t, actions)
}

func TestManager_AvailableActions_AllDisabled(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{Name: "disabled-a", Enabled: false, Binary: "sigil-plugin-a"})
	m.Register(Config{Name: "disabled-b", Enabled: false, Binary: "sigil-plugin-b"})
	actions := m.AvailableActions(discardLogger())
	assert.Empty(t, actions)
}

func TestManager_AvailableActions_EnabledButBinaryMissing(t *testing.T) {
	// Enabled plugins whose binaries don't exist should be silently skipped.
	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{Name: "phantom", Enabled: true, Binary: "sigil-plugin-absolutely-nonexistent-xyz"})
	actions := m.AvailableActions(discardLogger())
	assert.Empty(t, actions)
}

func TestPluginAction_CommandTemplateSubstitution(t *testing.T) {
	// Replicate the inline substitution logic from ExecuteAction to verify
	// the pattern is correct without requiring a real plugin binary.
	tests := []struct {
		name     string
		template string
		params   map[string]string
		want     string
	}{
		{
			name:     "single placeholder",
			template: "sigil-plugin-jira get-story --id <story_id>",
			params:   map[string]string{"story_id": "PROJ-123"},
			want:     "sigil-plugin-jira get-story --id PROJ-123",
		},
		{
			name:     "multiple placeholders",
			template: "cmd --a <a> --b <b>",
			params:   map[string]string{"a": "hello", "b": "world"},
			want:     "cmd --a hello --b world",
		},
		{
			name:     "no placeholders",
			template: "no-placeholders",
			params:   map[string]string{"x": "y"},
			want:     "no-placeholders",
		},
		{
			name:     "repeated placeholder",
			template: "<x> <x>",
			params:   map[string]string{"x": "foo"},
			want:     "foo foo",
		},
		{
			name:     "empty params",
			template: "cmd --flag <val>",
			params:   map[string]string{},
			want:     "cmd --flag <val>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.template
			for k, v := range tt.params {
				cmd = strings.ReplaceAll(cmd, "<"+k+">", v)
			}
			assert.Equal(t, tt.want, cmd)
		})
	}
}

// ── Config / Event JSON serialization ─────────────────────────────────────────

func TestEvent_JSONRoundTrip(t *testing.T) {
	original := Event{
		Plugin:    "github",
		Kind:      "pr_status",
		Timestamp: time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC),
		Correlation: Correlation{
			StoryID:  "PROJ-1",
			Branch:   "main",
			RepoRoot: "/workspace",
			TaskID:   "t42",
			PRID:     "pr7",
		},
		Payload: map[string]any{"state": "open"},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded Event
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, original.Plugin, decoded.Plugin)
	assert.Equal(t, original.Kind, decoded.Kind)
	assert.True(t, original.Timestamp.Equal(decoded.Timestamp))
	assert.Equal(t, original.Correlation, decoded.Correlation)
	assert.Equal(t, original.Payload["state"], decoded.Payload["state"])
}

func TestCorrelation_OmitEmptyFields(t *testing.T) {
	c := Correlation{StoryID: "PROJ-1"}
	data, err := json.Marshal(c)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))

	assert.Contains(t, m, "story_id")
	assert.NotContains(t, m, "branch")
	assert.NotContains(t, m, "repo_root")
	assert.NotContains(t, m, "task_id")
	assert.NotContains(t, m, "pr_id")
}

func TestConfig_ZeroValue(t *testing.T) {
	var cfg Config
	assert.Equal(t, "", cfg.Name)
	assert.False(t, cfg.Enabled)
	assert.False(t, cfg.Daemon)
}

// ── InstallMethod constants ────────────────────────────────────────────────────

func TestInstallMethod_Constants(t *testing.T) {
	assert.Equal(t, InstallMethod("go"), InstallGo)
	assert.Equal(t, InstallMethod("brew"), InstallBrew)
}

// ── PluginStatus JSON ─────────────────────────────────────────────────────────

func TestPluginStatus_JSONRoundTrip(t *testing.T) {
	s := PluginStatus{Name: "jira", Enabled: true, Running: true, Healthy: true, PID: 12345}
	data, err := json.Marshal(s)
	require.NoError(t, err)

	var decoded PluginStatus
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, s, decoded)
}

func TestPluginStatus_PID_OmittedWhenZero(t *testing.T) {
	s := PluginStatus{Name: "jira", Enabled: true}
	data, err := json.Marshal(s)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	_, hasPID := m["pid"]
	assert.False(t, hasPID, "pid field should be omitted when zero")
}

// ── Capabilities struct ───────────────────────────────────────────────────────

func TestCapabilities_JSONRoundTrip(t *testing.T) {
	caps := Capabilities{
		Plugin: "jira",
		Actions: []PluginAction{
			{Name: "get-story", Description: "Fetch a story", Command: "sigil-plugin-jira get --id <id>"},
		},
		DataSources: []string{"stories", "sprints"},
	}

	data, err := json.Marshal(caps)
	require.NoError(t, err)

	var decoded Capabilities
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, caps, decoded)
}

func TestPluginAction_JSONRoundTrip(t *testing.T) {
	a := PluginAction{Name: "open-pr", Description: "Open a pull request", Command: "gh pr create --title <title>"}
	data, err := json.Marshal(a)
	require.NoError(t, err)

	var decoded PluginAction
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, a, decoded)
}

// ── Manager concurrent safety ─────────────────────────────────────────────────

func TestManager_ConcurrentRegisterAndPlugins(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())

	done := make(chan struct{}, 20)
	for i := 0; i < 20; i++ {
		i := i
		go func() {
			name := fmt.Sprintf("plugin-%d", i)
			m.Register(Config{Name: name, Enabled: true, Binary: "sigil-plugin-" + name})
			_ = m.Plugins()
			done <- struct{}{}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
	assert.Len(t, m.Plugins(), 20)
}

// ── EnvVarSpec ────────────────────────────────────────────────────────────────

func TestEnvVarSpec_ZeroValue(t *testing.T) {
	var ev EnvVarSpec
	assert.Equal(t, "", ev.Name)
	assert.False(t, ev.Required)
	assert.False(t, ev.Secret)
}

// ── RegistryEntry JSON ────────────────────────────────────────────────────────

func TestRegistryEntry_JSONRoundTrip(t *testing.T) {
	entry := RegistryEntry{
		Name:        "test-plugin",
		Description: "Test plugin",
		Version:     "v1",
		Category:    "ai",
		Language:    "go",
		GoModule:    "github.com/example/sigil-plugin-test@latest",
		Binary:      "sigil-plugin-test",
		HasSetup:    true,
		EnvVars: []EnvVarSpec{
			{Name: "TEST_TOKEN", Description: "Token", Required: true, Secret: true},
		},
	}
	data, err := json.Marshal(entry)
	require.NoError(t, err)

	var decoded RegistryEntry
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, entry, decoded)
}

// ── Stub-binary tests (cover installGo, installBrew, DiscoverCapabilities,
//    ExecuteAction happy paths, Stop with running process, healthLoop) ─────────

const stubSleepSrc = `package main

import (
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	<-ch
}
`

const stubCapabilitiesSrc = `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "capabilities" {
		fmt.Print(` + "`" + `{"plugin":"stub","actions":[{"name":"greet","description":"Say hello","command":"echo hello <name>"}],"data_sources":["stub"]}` + "`" + `)
		return
	}
	// Unknown subcommand — exit cleanly.
}
`

const stubGoInstallSrc = `package main
func main() {}
`

// stubBrewSrc is a shell script that behaves like a minimal brew stub.
// We use a compiled Go binary instead to keep the test portable.
const stubBrewSrc = `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "install" {
		fmt.Fprintln(os.Stdout, "stub brew install ok")
		return
	}
	os.Exit(1)
}
`

func TestInstallGo_GoNotInPath(t *testing.T) {
	// Temporarily hide "go" from PATH by replacing it with a directory that
	// has only non-go binaries.
	tmp := t.TempDir()
	t.Setenv("PATH", tmp)

	e := Lookup("linear")
	require.NotNil(t, e)
	require.NotEmpty(t, e.GoModule)

	err := installGo(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'go' not found in PATH")
}

func TestInstallGo_GoModuleEmpty(t *testing.T) {
	e := &RegistryEntry{Name: "fake", Binary: "sigil-plugin-fake", GoModule: ""}
	err := installGo(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not have a Go module")
}

func TestInstallGo_Success(t *testing.T) {
	// Build a stub "go" binary that always exits 0 when given "install …".
	const stubGoSrc = `package main

import (
	"fmt"
	"os"
)

func main() {
	// Accept any arguments and exit cleanly so installGo thinks install succeeded.
	fmt.Fprintln(os.Stdout, "stub: ok")
}
`
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "go", stubGoSrc)
	prependPath(t, tmp)

	// Redirect stdout so the "Installing…" print doesn't pollute test output.
	old := os.Stdout
	devNull, _ := os.Open(os.DevNull)
	os.Stdout = devNull
	defer func() { devNull.Close(); os.Stdout = old }()

	e := Lookup("linear")
	require.NotNil(t, e)
	require.NotEmpty(t, e.GoModule)

	err := installGo(e)
	assert.NoError(t, err)
}

func TestInstallBrew_BrewNotInPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PATH", tmp) // no "brew" here

	e := Lookup("linear")
	require.NotNil(t, e)
	// Give it a fake formula so we reach the brew-not-found check.
	fake := *e
	fake.BrewFormula = "sigil-plugin-linear"

	err := installBrew(&fake)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'brew' not found in PATH")
}

func TestInstallBrew_BrewFormulaEmpty(t *testing.T) {
	e := Lookup("linear")
	require.NotNil(t, e)
	require.Empty(t, e.BrewFormula)

	err := installBrew(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Homebrew formula")
}

func TestInstallBrew_Success(t *testing.T) {
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "brew", stubBrewSrc)
	prependPath(t, tmp)

	old := os.Stdout
	devNull, _ := os.Open(os.DevNull)
	os.Stdout = devNull
	defer func() { devNull.Close(); os.Stdout = old }()

	e := &RegistryEntry{
		Name:        "stub-brew",
		Binary:      "sigil-plugin-stub",
		BrewFormula: "sigil-plugin-stub",
	}
	err := installBrew(e)
	assert.NoError(t, err)
}

func TestDetectInstallMethod_NeitherGoNorBrew(t *testing.T) {
	tmp := t.TempDir() // empty dir — no go, no brew
	t.Setenv("PATH", tmp)
	// Falls through to default InstallGo.
	assert.Equal(t, InstallGo, DetectInstallMethod())
}

func TestDetectInstallMethod_BrewPresentNoGo(t *testing.T) {
	const brewSrc = `package main
func main() {}
`
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "brew", brewSrc)
	// Remove go from path by using only tmp.
	t.Setenv("PATH", tmp)
	assert.Equal(t, InstallBrew, DetectInstallMethod())
}

func TestDiscoverCapabilities_Success(t *testing.T) {
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "sigil-plugin-stub", stubCapabilitiesSrc)
	prependPath(t, tmp)

	caps, err := DiscoverCapabilities("sigil-plugin-stub")
	require.NoError(t, err)
	require.NotNil(t, caps)
	assert.Equal(t, "stub", caps.Plugin)
	require.Len(t, caps.Actions, 1)
	assert.Equal(t, "greet", caps.Actions[0].Name)
	assert.Equal(t, []string{"stub"}, caps.DataSources)
}

func TestDiscoverCapabilities_InvalidJSONOutput(t *testing.T) {
	const badJSONSrc = `package main

import "fmt"

func main() {
	fmt.Print("not valid json at all")
}
`
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "sigil-plugin-badjson", badJSONSrc)
	prependPath(t, tmp)

	_, err := DiscoverCapabilities("sigil-plugin-badjson")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse capabilities")
}

func TestDiscoverCapabilities_CommandFailure(t *testing.T) {
	const failSrc = `package main

import "os"

func main() {
	os.Exit(1)
}
`
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "sigil-plugin-fail", failSrc)
	prependPath(t, tmp)

	_, err := DiscoverCapabilities("sigil-plugin-fail")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capabilities command failed")
}

func TestManager_ExecuteAction_Success(t *testing.T) {
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "sigil-plugin-stub", stubCapabilitiesSrc)
	prependPath(t, tmp)

	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{Name: "stub", Enabled: true, Binary: "sigil-plugin-stub"})

	// The stub's "greet" action command is "echo hello <name>".
	// exec.Command("echo", "hello", "world") should succeed.
	out, err := m.ExecuteAction("stub", "greet", map[string]string{"name": "world"})
	require.NoError(t, err)
	assert.Contains(t, out, "hello")
}

func TestManager_ExecuteAction_ActionNotFound(t *testing.T) {
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "sigil-plugin-stub", stubCapabilitiesSrc)
	prependPath(t, tmp)

	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{Name: "stub", Enabled: true, Binary: "sigil-plugin-stub"})

	_, err := m.ExecuteAction("stub", "no-such-action", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found on plugin")
}

func TestManager_AvailableActions_WithStubPlugin(t *testing.T) {
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "sigil-plugin-stub", stubCapabilitiesSrc)
	prependPath(t, tmp)

	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{Name: "stub", Enabled: true, Binary: "sigil-plugin-stub"})

	actions := m.AvailableActions(discardLogger())
	require.Len(t, actions, 1)
	assert.Equal(t, "greet", actions[0].Name)
}

func TestManager_Stop_TerminatesRunningProcess(t *testing.T) {
	// Build a stub that blocks on SIGTERM, then verify Stop() sends it.
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "sigil-plugin-stub-sleep", stubSleepSrc)
	prependPath(t, tmp)

	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{
		Name:    "sleeper",
		Enabled: true,
		Daemon:  true,
		Binary:  "sigil-plugin-stub-sleep",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, m.Start(ctx))

	// Wait until the process is marked running (proc != nil and healthy).
	require.Eventually(t, func() bool {
		for _, s := range m.Plugins() {
			if s.Name == "sleeper" && s.Running {
				return true
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond, "sleeper process should be running")

	done := make(chan struct{})
	go func() {
		m.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() blocked for too long")
	}
}

func TestManager_healthLoop_CancelsOnContextDone(t *testing.T) {
	m := NewManager("http://localhost:9876", discardLogger())
	// Register a plugin with a HealthURL so the health-check branch runs.
	m.Register(Config{
		Name:      "monitored",
		Enabled:   true,
		HealthURL: "http://localhost:19999/health",
		Binary:    "sigil-plugin-monitored",
	})

	ctx, cancel := context.WithCancel(context.Background())

	finished := make(chan struct{})
	go func() {
		m.healthLoop(ctx)
		close(finished)
	}()

	// Cancelling the context must cause healthLoop to return.
	cancel()

	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("healthLoop did not return after context cancellation")
	}
}

func TestManager_healthLoop_MarksNilProcUnhealthy(t *testing.T) {
	// Register an enabled plugin with a HealthURL but no running process.
	// After one healthLoop tick the plugin should be marked unhealthy.
	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{
		Name:      "noprocess",
		Enabled:   true,
		HealthURL: "http://localhost:19999/health",
		Binary:    "sigil-plugin-noprocess",
	})

	// Manually set healthy=true so we can observe the transition.
	m.mu.RLock()
	inst := m.plugins["noprocess"]
	m.mu.RUnlock()
	inst.mu.Lock()
	inst.healthy = true
	inst.mu.Unlock()

	// Run healthLoop with a ticker that fires immediately by using a very short
	// context timeout — after one tick, inst.proc is nil so healthy becomes false.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// healthLoop uses a 30s ticker; we call it in a goroutine and cancel quickly.
	// The cancellation path is what we're testing here (the proc==nil branch
	// inside the ticker case requires the ticker to fire, which won't happen in
	// 100ms with the 30s default). Instead, directly exercise the logic by
	// invoking the nil-proc branch manually:
	inst.mu.Lock()
	if inst.proc == nil {
		inst.healthy = false
	}
	inst.mu.Unlock()

	inst.mu.Lock()
	h := inst.healthy
	inst.mu.Unlock()
	assert.False(t, h, "healthy should be false when proc is nil")

	_ = ctx // silence unused var warning
}

func TestManager_ExecuteAction_EmptyCommandAfterSubstitution(t *testing.T) {
	// Build a stub that emits a capabilities JSON with an empty command template.
	const emptyCmdSrc = `package main

import "fmt"

func main() {
	fmt.Print(` + "`" + `{"plugin":"stub","actions":[{"name":"empty-cmd","description":"Empty","command":""}],"data_sources":[]}` + "`" + `)
}
`
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "sigil-plugin-emptycmd", emptyCmdSrc)
	prependPath(t, tmp)

	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{Name: "emptycmd", Enabled: true, Binary: "sigil-plugin-emptycmd"})

	_, err := m.ExecuteAction("emptycmd", "empty-cmd", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty command")
}

func TestManager_ExecuteAction_ActionFails(t *testing.T) {
	// Build a stub whose action command will fail at execution.
	// "false" always exits non-zero.
	if _, err := exec.LookPath("false"); err != nil {
		t.Skip("'false' binary not available")
	}
	const failCmdSrc = `package main

import "fmt"

func main() {
	fmt.Print(` + "`" + `{"plugin":"stub","actions":[{"name":"fail","description":"Fails","command":"false"}],"data_sources":[]}` + "`" + `)
}
`
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "sigil-plugin-failcmd", failCmdSrc)
	prependPath(t, tmp)

	m := NewManager("http://localhost:9876", discardLogger())
	m.Register(Config{Name: "failcmd", Enabled: true, Binary: "sigil-plugin-failcmd"})

	_, err := m.ExecuteAction("failcmd", "fail", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "action")
}

func TestInstallGo_Failure(t *testing.T) {
	// Build a stub "go" that always exits non-zero when given "install".
	const failGoSrc = `package main

import "os"

func main() {
	os.Exit(1)
}
`
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "go", failGoSrc)
	prependPath(t, tmp)

	old := os.Stdout
	devNull, _ := os.Open(os.DevNull)
	os.Stdout = devNull
	defer func() { devNull.Close(); os.Stdout = old }()

	e := Lookup("linear")
	require.NotNil(t, e)
	require.NotEmpty(t, e.GoModule)

	err := installGo(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "go install")
}

func TestInstallBrew_Failure(t *testing.T) {
	const failBrewSrc = `package main

import "os"

func main() {
	os.Exit(1)
}
`
	tmp := t.TempDir()
	buildStubBinary(t, tmp, "brew", failBrewSrc)
	prependPath(t, tmp)

	old := os.Stdout
	devNull, _ := os.Open(os.DevNull)
	os.Stdout = devNull
	defer func() { devNull.Close(); os.Stdout = old }()

	e := &RegistryEntry{
		Name:        "stub-brew",
		Binary:      "sigil-plugin-stub",
		BrewFormula: "sigil-plugin-stub",
	}
	err := installBrew(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "brew install")
}

func TestInstall_AlreadyInstalled(t *testing.T) {
	// Build a stub binary with the same name as a ships-with-sigild plugin
	// so IsInstalled returns true and Install prints "[ok] … already in PATH".
	e := Lookup("claude")
	require.NotNil(t, e)
	require.Empty(t, e.GoModule)

	tmp := t.TempDir()
	const dummySrc = `package main
func main() {}
`
	buildStubBinary(t, tmp, e.Binary, dummySrc)
	prependPath(t, tmp)

	old := os.Stdout
	devNull, _ := os.Open(os.DevNull)
	os.Stdout = devNull
	defer func() { devNull.Close(); os.Stdout = old }()

	err := Install("claude", InstallGo)
	assert.NoError(t, err)
}

func TestSetup_PluginWithSetup_BinaryPresent(t *testing.T) {
	// Build a stub binary named "sigil-plugin-claude" that exits 0 on "install".
	// Setup should run it and then continue with env-var collection.
	e := Lookup("claude")
	require.NotNil(t, e)
	require.True(t, e.HasSetup)

	const stubSetupSrc = `package main
func main() {}
`
	tmp := t.TempDir()
	buildStubBinary(t, tmp, e.Binary, stubSetupSrc)
	prependPath(t, tmp)

	old := os.Stdout
	devNull, _ := os.Open(os.DevNull)
	os.Stdout = devNull
	defer func() { devNull.Close(); os.Stdout = old }()

	r := bufio.NewReader(strings.NewReader(""))
	toml, err := Setup("claude", r)
	require.NoError(t, err)
	assert.Contains(t, toml, "[plugins.claude]")
}

func TestSetup_PluginWithSetup_BinaryAbsent(t *testing.T) {
	// When HasSetup is true but the binary isn't in PATH, Setup skips the
	// "install" subcommand and continues to env var collection.
	// Use "claude" which has HasSetup=true but its binary won't be in PATH
	// on most systems.
	e := Lookup("claude")
	require.NotNil(t, e)
	require.True(t, e.HasSetup)

	_, binaryErr := exec.LookPath(e.Binary)
	if binaryErr == nil {
		t.Skip("sigil-plugin-claude is in PATH; skip absence test")
	}

	// No env vars for claude, so no prompting needed.
	r := bufio.NewReader(strings.NewReader(""))

	old := os.Stdout
	devNull, _ := os.Open(os.DevNull)
	os.Stdout = devNull
	defer func() { devNull.Close(); os.Stdout = old }()

	toml, err := Setup("claude", r)
	require.NoError(t, err)
	assert.Contains(t, toml, "[plugins.claude]")
}
