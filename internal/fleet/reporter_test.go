package fleet

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/config"
	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	tmp := t.TempDir()
	s, err := store.Open(tmp + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

func TestReporterPreview(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// Insert some AI interactions
	for i := 0; i < 5; i++ {
		_ = s.InsertAIInteraction(ctx, event.AIInteraction{
			QueryCategory: "code_gen",
			Routing:       "local",
			LatencyMS:     100,
			Timestamp:     time.Now(),
		})
	}
	for i := 0; i < 3; i++ {
		_ = s.InsertAIInteraction(ctx, event.AIInteraction{
			QueryCategory: "debug",
			Routing:       "cloud",
			LatencyMS:     200,
			Timestamp:     time.Now(),
		})
	}

	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: "http://localhost:9999",
		Interval: "1h",
	}, testLogger())

	report, err := r.Preview(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if report.NodeID == "" {
		t.Error("expected non-empty node ID")
	}
	if report.AIQueryCounts["code_gen"] != 5 {
		t.Errorf("expected 5 code_gen queries, got %d", report.AIQueryCounts["code_gen"])
	}
	if report.AIQueryCounts["debug"] != 3 {
		t.Errorf("expected 3 debug queries, got %d", report.AIQueryCounts["debug"])
	}
	// 5 local out of 8 total
	expectedRatio := 5.0 / 8.0
	if report.LocalRoutingRatio < expectedRatio-0.01 || report.LocalRoutingRatio > expectedRatio+0.01 {
		t.Errorf("expected routing ratio ~%.2f, got %.2f", expectedRatio, report.LocalRoutingRatio)
	}
}

func TestReporterOptOut(t *testing.T) {
	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: "http://localhost:9999",
	}, testLogger())

	if !r.Enabled() {
		t.Error("expected reporter to be enabled")
	}

	r.OptOut()

	if r.Enabled() {
		t.Error("expected reporter to be disabled after opt-out")
	}
	if r.QueueSize() != 0 {
		t.Error("expected empty queue after opt-out")
	}
}

func TestDetectAdoptionTier(t *testing.T) {
	tests := []struct {
		queries    int
		acceptRate float64
		want       int
	}{
		{0, 0, 0},
		{3, 0.1, 1},
		{10, 0.3, 2},
		{25, 0.6, 3},
	}
	for _, tt := range tests {
		got := detectAdoptionTier(tt.queries, tt.acceptRate)
		if got != tt.want {
			t.Errorf("detectAdoptionTier(%d, %.1f) = %d, want %d",
				tt.queries, tt.acceptRate, got, tt.want)
		}
	}
}

func TestIsBuildCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go build ./...", true},
		{"go test -v ./...", true},
		{"make all", true},
		{"cargo build", true},
		{"npm run build", true},
		{"ls -la", false},
		{"git commit", false},
	}
	for _, tt := range tests {
		if got := isBuildCommand(tt.cmd); got != tt.want {
			t.Errorf("isBuildCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestGenerateUUID(t *testing.T) {
	id := generateUUID()
	if len(id) != 36 {
		t.Errorf("expected UUID length 36, got %d: %s", len(id), id)
	}
	// Check version nibble
	if id[14] != '4' {
		t.Errorf("expected version 4, got %c", id[14])
	}
}

// TestEnabled verifies that Enabled reflects the initial config value.
func TestEnabled(t *testing.T) {
	s := testStore(t)

	enabled := New(s, config.FleetConfig{Enabled: true}, testLogger())
	if !enabled.Enabled() {
		t.Error("expected Enabled() == true when config.Enabled is true")
	}

	disabled := New(s, config.FleetConfig{Enabled: false}, testLogger())
	if disabled.Enabled() {
		t.Error("expected Enabled() == false when config.Enabled is false")
	}
}

// TestQueueSize verifies that a freshly created reporter has an empty queue.
func TestQueueSize(t *testing.T) {
	s := testStore(t)
	r := New(s, config.FleetConfig{Enabled: true, Endpoint: "http://localhost:9999"}, testLogger())
	if n := r.QueueSize(); n != 0 {
		t.Errorf("expected QueueSize() == 0, got %d", n)
	}
}

// TestRun_cancellation verifies that Run returns promptly when given an
// already-cancelled context, without blocking on the ticker.
func TestRun_cancellation(t *testing.T) {
	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: "http://localhost:9999",
		Interval: "1h",
	}, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling Run

	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// returned promptly — correct
	case <-time.After(2 * time.Second):
		t.Error("Run did not return after context cancellation")
	}
}

// TestRun_disabledReturnsImmediately verifies that Run exits immediately when
// the reporter is disabled, without waiting for any ticker or context signal.
func TestRun_disabledReturnsImmediately(t *testing.T) {
	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  false,
		Endpoint: "http://localhost:9999",
		Interval: "1h",
	}, testLogger())

	done := make(chan struct{})
	go func() {
		r.Run(context.Background())
		close(done)
	}()

	select {
	case <-done:
		// returned immediately because disabled — correct
	case <-time.After(2 * time.Second):
		t.Error("Run did not return immediately for disabled reporter")
	}
}

// TestComputeBuildSuccessRate verifies that computeBuildSuccessRate correctly
// counts build commands and their exit codes.
func TestComputeBuildSuccessRate(t *testing.T) {
	tests := []struct {
		name     string
		events   []struct {
			cmd      string
			exitCode any // float64 simulates JSON-decoded number; int tests int branch
		}
		want float64
	}{
		{
			name: "all successful",
			events: []struct {
				cmd      string
				exitCode any
			}{
				{"go build ./...", float64(0)},
				{"go test ./...", float64(0)},
			},
			want: 1.0,
		},
		{
			name: "all failed",
			events: []struct {
				cmd      string
				exitCode any
			}{
				{"go build ./...", float64(1)},
				{"go build ./...", float64(2)},
			},
			want: 0.0,
		},
		{
			name: "mixed success and failure",
			events: []struct {
				cmd      string
				exitCode any
			}{
				{"go build ./...", float64(0)},
				{"go build ./...", float64(1)},
				{"go test ./...", float64(0)},
				{"go test ./...", float64(1)},
			},
			want: 0.5,
		},
		{
			name: "non-build commands excluded",
			events: []struct {
				cmd      string
				exitCode any
			}{
				{"ls -la", float64(0)},
				{"git status", float64(0)},
				{"go build ./...", float64(0)},
			},
			want: 1.0,
		},
		{
			name: "exit_code as int type",
			events: []struct {
				cmd      string
				exitCode any
			}{
				{"go build ./...", int(0)},
				{"go build ./...", int(1)},
			},
			want: 0.5,
		},
		{
			name:   "no build events",
			events: nil,
			want:   0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()

			for _, ev := range tt.events {
				err := s.InsertEvent(ctx, event.Event{
					Kind:   event.KindTerminal,
					Source: "test",
					Payload: map[string]any{
						"cmd":       ev.cmd,
						"exit_code": ev.exitCode,
					},
					Timestamp: time.Now(),
				})
				if err != nil {
					t.Fatalf("InsertEvent: %v", err)
				}
			}

			r := New(s, config.FleetConfig{
				Enabled:  true,
				Interval: "1h",
			}, testLogger())

			since := time.Now().Add(-2 * time.Hour)
			got := r.computeBuildSuccessRate(ctx, since)
			if got < tt.want-0.001 || got > tt.want+0.001 {
				t.Errorf("computeBuildSuccessRate() = %f, want %f", got, tt.want)
			}
		})
	}
}

// TestPreview_buildSuccessRate verifies that Preview populates BuildSuccessRate
// correctly when the store contains a mix of successful and failed build events.
func TestPreview_buildSuccessRate(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	buildEvents := []struct {
		cmd      string
		exitCode float64
	}{
		{"go build ./...", 0},
		{"go build ./...", 0},
		{"go test ./...", 1},
		{"make all", 0},
	}
	for _, be := range buildEvents {
		err := s.InsertEvent(ctx, event.Event{
			Kind:   event.KindTerminal,
			Source: "test",
			Payload: map[string]any{
				"cmd":       be.cmd,
				"exit_code": be.exitCode,
			},
			Timestamp: time.Now(),
		})
		if err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	r := New(s, config.FleetConfig{
		Enabled:  true,
		Interval: "1h",
	}, testLogger())

	report, err := r.Preview(ctx)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	// 3 successes out of 4 build commands = 0.75
	const want = 0.75
	if report.BuildSuccessRate < want-0.001 || report.BuildSuccessRate > want+0.001 {
		t.Errorf("BuildSuccessRate = %f, want %f", report.BuildSuccessRate, want)
	}
}

// TestPostReport verifies that postReport sends a well-formed POST request to
// the fleet endpoint and clears no error on HTTP 200.
func TestPostReport(t *testing.T) {
	var received atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", req.Method)
		}
		if req.URL.Path != "/api/v1/reports" {
			t.Errorf("unexpected path: %s", req.URL.Path)
		}
		if ct := req.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}

		var report FleetReport
		if err := json.NewDecoder(req.Body).Decode(&report); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: srv.URL,
		Interval: "1h",
	}, testLogger())

	report := FleetReport{
		NodeID:    "test-node",
		Timestamp: time.Now(),
	}

	if err := r.postReport(context.Background(), report); err != nil {
		t.Fatalf("postReport returned unexpected error: %v", err)
	}
	if received.Load() != 1 {
		t.Errorf("expected server to receive 1 request, got %d", received.Load())
	}
}

// TestPostReport_serverError verifies that postReport returns an error when
// the fleet endpoint responds with a non-2xx status code.
func TestPostReport_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: srv.URL,
	}, testLogger())

	err := r.postReport(context.Background(), FleetReport{NodeID: "x"})
	if err == nil {
		t.Error("expected error for 500 response, got nil")
	}
}

// TestPostReport_noEndpoint verifies that postReport returns an error when no
// fleet endpoint is configured.
func TestPostReport_noEndpoint(t *testing.T) {
	s := testStore(t)
	r := New(s, config.FleetConfig{Enabled: true}, testLogger())

	err := r.postReport(context.Background(), FleetReport{NodeID: "x"})
	if err == nil {
		t.Error("expected error when endpoint is empty, got nil")
	}
}

// TestSendQueued_success verifies that sendQueued drains the queue when the
// server accepts every report.
func TestSendQueued_success(t *testing.T) {
	var received atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: srv.URL,
		Interval: "1h",
	}, testLogger())

	// Manually enqueue two reports.
	r.mu.Lock()
	r.queue = append(r.queue,
		FleetReport{NodeID: "node-1", Timestamp: time.Now()},
		FleetReport{NodeID: "node-2", Timestamp: time.Now()},
	)
	r.mu.Unlock()

	r.sendQueued(context.Background())

	if n := r.QueueSize(); n != 0 {
		t.Errorf("expected empty queue after sendQueued, got %d", n)
	}
	if n := received.Load(); n != 2 {
		t.Errorf("expected server to receive 2 reports, got %d", n)
	}
}

// TestSendQueued_retainsOnFailure verifies that sendQueued stops at the first
// failed report and leaves unsent reports in the queue.
func TestSendQueued_retainsOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: srv.URL,
		Interval: "1h",
	}, testLogger())

	r.mu.Lock()
	r.queue = append(r.queue,
		FleetReport{NodeID: "node-1"},
		FleetReport{NodeID: "node-2"},
	)
	r.mu.Unlock()

	r.sendQueued(context.Background())

	// Both reports must remain because the first send failed.
	if n := r.QueueSize(); n != 2 {
		t.Errorf("expected 2 reports retained in queue, got %d", n)
	}
}

// TestCycle verifies that cycle appends a report to the queue and then drains
// it when the server is reachable.
func TestCycle(t *testing.T) {
	var received atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: srv.URL,
		Interval: "1h",
	}, testLogger())

	r.cycle(context.Background())

	// After a successful cycle the queue should be empty.
	if n := r.QueueSize(); n != 0 {
		t.Errorf("expected empty queue after cycle, got %d", n)
	}
	if n := received.Load(); n != 1 {
		t.Errorf("expected server to receive 1 report, got %d", n)
	}
}

// TestFetchPolicy verifies that fetchPolicy stores the policy returned by the
// fleet endpoint and makes it available via CurrentPolicy.
func TestFetchPolicy(t *testing.T) {
	want := RoutingPolicy{
		RoutingMode:      "local",
		AllowedProviders: []string{"ollama"},
		AllowedModelIDs:  []string{"llama3"},
		EnforcedAt:       time.Now().UTC().Truncate(time.Second),
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v1/policy" {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: srv.URL,
	}, testLogger())

	if r.CurrentPolicy() != nil {
		t.Fatal("expected nil policy before fetchPolicy")
	}

	r.fetchPolicy(context.Background())

	got := r.CurrentPolicy()
	if got == nil {
		t.Fatal("expected non-nil policy after fetchPolicy")
	}
	if got.RoutingMode != want.RoutingMode {
		t.Errorf("RoutingMode = %q, want %q", got.RoutingMode, want.RoutingMode)
	}
	if len(got.AllowedProviders) != 1 || got.AllowedProviders[0] != "ollama" {
		t.Errorf("AllowedProviders = %v, want [ollama]", got.AllowedProviders)
	}
}

// TestFetchPolicy_nonOKStatus verifies that a non-200 policy response leaves
// the current policy unchanged.
func TestFetchPolicy_nonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: srv.URL,
	}, testLogger())

	r.fetchPolicy(context.Background())

	if r.CurrentPolicy() != nil {
		t.Error("expected nil policy after non-200 response")
	}
}

// TestFetchPolicy_emptyEndpoint verifies that fetchPolicy is a no-op when no
// endpoint is configured, avoiding any network dial.
func TestFetchPolicy_emptyEndpoint(t *testing.T) {
	s := testStore(t)
	r := New(s, config.FleetConfig{Enabled: true}, testLogger())

	// Should return without panicking or dialling.
	r.fetchPolicy(context.Background())

	if r.CurrentPolicy() != nil {
		t.Error("expected nil policy when endpoint is empty")
	}
}

// TestFetchPolicy_badJSON verifies that a malformed JSON response from the
// policy endpoint leaves the current policy unchanged.
func TestFetchPolicy_badJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not valid json{{{`))
	}))
	defer srv.Close()

	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: srv.URL,
	}, testLogger())

	r.fetchPolicy(context.Background())

	if r.CurrentPolicy() != nil {
		t.Error("expected nil policy after malformed JSON response")
	}
}

// TestSendQueued_emptyQueue verifies that sendQueued is a no-op and returns
// cleanly when the queue is empty.
func TestSendQueued_emptyQueue(t *testing.T) {
	s := testStore(t)
	// Point at a server that should never be contacted.
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: "http://127.0.0.1:0", // nothing listening here
		Interval: "1h",
	}, testLogger())

	// Queue is empty; sendQueued must return without attempting any HTTP call.
	r.sendQueued(context.Background())

	if n := r.QueueSize(); n != 0 {
		t.Errorf("expected empty queue, got %d", n)
	}
}

// TestCycle_storeError verifies that cycle logs and returns without enqueuing
// a report when the underlying store is closed and computeReport fails.
func TestCycle_storeError(t *testing.T) {
	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: "http://localhost:9999",
		Interval: "1h",
	}, testLogger())

	// Close the store to force QueryAIInteractions to return an error.
	s.Close()

	r.cycle(context.Background())

	// No report should have been enqueued because computeReport failed.
	if n := r.QueueSize(); n != 0 {
		t.Errorf("expected empty queue after store error, got %d", n)
	}
}

// TestComputeBuildSuccessRate_storeError verifies that computeBuildSuccessRate
// returns 0 when QueryTerminalEvents fails (e.g. closed store).
func TestComputeBuildSuccessRate_storeError(t *testing.T) {
	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Interval: "1h",
	}, testLogger())

	s.Close()

	got := r.computeBuildSuccessRate(context.Background(), time.Now().Add(-time.Hour))
	if got != 0 {
		t.Errorf("expected 0 on store error, got %f", got)
	}
}

// TestLoadOrCreateNodeID_existingFile verifies that loadOrCreateNodeID reads
// an existing node_id file instead of generating a new UUID.
func TestLoadOrCreateNodeID_existingFile(t *testing.T) {
	dir := t.TempDir()
	nodeDir := dir + "/sigild"
	if err := os.MkdirAll(nodeDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	const wantID = "aaaabbbb-cccc-4ddd-8eee-ffffffffffff"
	if err := os.WriteFile(nodeDir+"/node_id", []byte(wantID), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("XDG_DATA_HOME", dir)

	got := loadOrCreateNodeID(testLogger())
	if got != wantID {
		t.Errorf("loadOrCreateNodeID() = %q, want %q", got, wantID)
	}
}

// TestLoadOrCreateNodeID_createsFile verifies that loadOrCreateNodeID writes a
// new UUID to disk when no node_id file exists yet.
func TestLoadOrCreateNodeID_createsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	id := loadOrCreateNodeID(testLogger())
	if len(id) != 36 {
		t.Errorf("expected UUID length 36, got %d: %s", len(id), id)
	}

	// Calling again must return the same persisted ID.
	id2 := loadOrCreateNodeID(testLogger())
	if id != id2 {
		t.Errorf("second call returned different ID: %q vs %q", id, id2)
	}
}

// TestRun_tickerCycle verifies that Run fires a cycle when the ticker elapses
// by using a very short interval and a real httptest server.
func TestRun_tickerCycle(t *testing.T) {
	var received atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/v1/reports":
			received.Add(1)
			w.WriteHeader(http.StatusOK)
		case "/api/v1/policy":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"routing_mode":"local"}`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer srv.Close()

	s := testStore(t)
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: srv.URL,
		Interval: "50ms",
	}, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	r.Run(ctx)

	if received.Load() == 0 {
		t.Error("expected at least one report sent during Run")
	}
}
