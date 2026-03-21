package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/inference"
	"github.com/wambozi/sigil/internal/notifier"
	"github.com/wambozi/sigil/internal/store"
)

// --- helpers ---------------------------------------------------------------

func openMemoryStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// mockInferenceServer creates a minimal OpenAI-compatible chat completions
// server for use in analyzer tests that need a working inference engine.
func mockInferenceServer(content string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{"message": map[string]any{"role": "assistant", "content": content}},
				},
			})
		case "/v1/models", "/health":
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

// newTestEngine creates an inference.Engine backed by the given mock server.
func newTestEngine(t *testing.T, serverURL string) *inference.Engine {
	t.Helper()
	engine, err := inference.New(inference.Config{
		Mode: "local",
		Local: inference.LocalConfig{
			Enabled:   true,
			ServerURL: serverURL,
		},
	}, newTestLogger())
	if err != nil {
		t.Fatalf("inference.New: %v", err)
	}
	return engine
}

// --- buildPrompt -----------------------------------------------------------

func TestBuildPrompt_containsEventCounts(t *testing.T) {
	s := &Summary{
		Period: time.Hour,
		EventCounts: map[event.Kind]int64{
			event.KindFile:    12,
			event.KindProcess: 5,
			event.KindGit:     3,
			event.KindAI:      1,
		},
	}

	prompt := buildPrompt(s)

	checks := []string{"1h0m0s", "12", "5", "3", "1"}
	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\nprompt:\n%s", want, prompt)
		}
	}
}

func TestBuildPrompt_zeroCounts(t *testing.T) {
	s := &Summary{
		Period:      30 * time.Minute,
		EventCounts: map[event.Kind]int64{},
	}
	prompt := buildPrompt(s)
	if prompt == "" {
		t.Error("buildPrompt returned empty string for zero-count summary")
	}
}

func TestBuildPrompt_withTopFilesAndSuggestions(t *testing.T) {
	s := &Summary{
		Period: time.Hour,
		EventCounts: map[event.Kind]int64{
			event.KindFile:     10,
			event.KindProcess:  2,
			event.KindGit:      1,
			event.KindTerminal: 5,
			event.KindAI:       3,
		},
		TopFiles: []store.FileEditCount{
			{Path: "/proj/main.go", Count: 8},
			{Path: "/proj/handler.go", Count: 4},
		},
	}

	prompt := buildPrompt(s)
	for _, want := range []string{"main.go", "handler.go", "1h0m0s"} {
		found := false
		for i := 0; i+len(want) <= len(prompt); i++ {
			if prompt[i:i+len(want)] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestBuildPrompt_withAIInteractions(t *testing.T) {
	s := &Summary{
		Period:      30 * time.Minute,
		EventCounts: map[event.Kind]int64{},
		AIInteractions: []event.AIInteraction{
			{QueryCategory: "debug", Timestamp: time.Now()},
			{QueryCategory: "debug", Timestamp: time.Now()},
			{QueryCategory: "explain", Timestamp: time.Now()},
		},
		AcceptanceRate: 0.75,
	}

	prompt := buildPrompt(s)
	for _, want := range []string{"3", "75%", "debug"} {
		found := false
		for i := 0; i+len(want) <= len(prompt); i++ {
			if prompt[i:i+len(want)] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("prompt missing %q\nfull prompt:\n%s", want, prompt)
		}
	}
}

func TestBuildPrompt_longPromptTruncated(t *testing.T) {
	// Build a summary that exceeds 7500 chars by adding many long suggestions.
	longBody := make([]byte, 200)
	for i := range longBody {
		longBody[i] = 'x'
	}

	var suggestions []notifier.Suggestion
	for i := range 50 {
		suggestions = append(suggestions, notifier.Suggestion{
			Title: fmt.Sprintf("Pattern %d", i),
			Body:  string(longBody),
		})
	}

	// Build a very long top-files list too.
	var files []store.FileEditCount
	for i := range 20 {
		files = append(files, store.FileEditCount{
			Path:  fmt.Sprintf("/very/long/path/to/some/file_%d.go", i),
			Count: int64(i + 1),
		})
	}

	s := &Summary{
		Period:      time.Hour,
		EventCounts: map[event.Kind]int64{event.KindFile: 100},
		Suggestions: suggestions,
		TopFiles:    files,
	}

	prompt := buildPrompt(s)
	if len(prompt) > 7500+500 {
		// Allow some slack for the truncation header line.
		t.Errorf("truncated prompt is %d chars; expected <= ~8000", len(prompt))
	}
	if prompt == "" {
		t.Error("buildPrompt returned empty string")
	}
}

func TestBuildPrompt_buildHealthSection(t *testing.T) {
	// Trigger the "build health" section by having terminal events.
	s := &Summary{
		Period: time.Hour,
		EventCounts: map[event.Kind]int64{
			event.KindTerminal: 10,
		},
	}

	prompt := buildPrompt(s)
	if prompt == "" {
		t.Error("buildPrompt returned empty string with terminal events")
	}
}

func TestBuildPrompt_buildHealthWithBuildFailureSuggestion(t *testing.T) {
	// Include a suggestion whose title contains "build/test failures" to
	// exercise the buildCmds counter branch in buildPrompt.
	s := &Summary{
		Period: time.Hour,
		EventCounts: map[event.Kind]int64{
			event.KindTerminal: 5,
		},
		Suggestions: []notifier.Suggestion{
			{Title: "Repeated build/test failures", Body: "3 consecutive failures"},
		},
	}

	prompt := buildPrompt(s)
	if prompt == "" {
		t.Error("buildPrompt returned empty string")
	}
}

// --- localPass -------------------------------------------------------------

func TestLocalPass_countsMatchInserted(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	interval := time.Hour
	now := time.Now()

	insert := func(k event.Kind, ts time.Time) {
		t.Helper()
		err := db.InsertEvent(ctx, event.Event{
			Kind: k, Source: "test",
			Payload:   map[string]any{},
			Timestamp: ts,
		})
		if err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	// Within the analysis window.
	insert(event.KindFile, now.Add(-30*time.Minute))
	insert(event.KindFile, now.Add(-45*time.Minute))
	insert(event.KindGit, now.Add(-10*time.Minute))
	// Outside the window — should not be counted.
	insert(event.KindFile, now.Add(-2*time.Hour))

	a := New(db, nil, interval, newTestLogger())
	summary, err := a.localPass(ctx)
	if err != nil {
		t.Fatalf("localPass: %v", err)
	}

	if summary.EventCounts[event.KindFile] != 2 {
		t.Errorf("file events: got %d, want 2", summary.EventCounts[event.KindFile])
	}
	if summary.EventCounts[event.KindGit] != 1 {
		t.Errorf("git events: got %d, want 1", summary.EventCounts[event.KindGit])
	}
	if summary.Period != interval {
		t.Errorf("Period: got %v, want %v", summary.Period, interval)
	}
}

func TestLocalPass_emptyStore(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	a := New(db, nil, time.Hour, newTestLogger())
	summary, err := a.localPass(ctx)
	if err != nil {
		t.Fatalf("localPass on empty store: %v", err)
	}
	for k, n := range summary.EventCounts {
		if n != 0 {
			t.Errorf("expected 0 for %s, got %d", k, n)
		}
	}
}

func TestLocalPass_topFilesAndAcceptanceRate(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Insert file events.
	for i := range 5 {
		if err := db.InsertEvent(ctx, event.Event{
			Kind:      event.KindFile,
			Source:    "test",
			Payload:   map[string]any{"path": "/proj/hot.go"},
			Timestamp: now.Add(-time.Duration(i+1) * 5 * time.Minute),
		}); err != nil {
			t.Fatalf("InsertEvent file: %v", err)
		}
	}

	a := New(db, nil, time.Hour, newTestLogger())
	summary, err := a.localPass(ctx)
	if err != nil {
		t.Fatalf("localPass: %v", err)
	}

	if summary.EventCounts[event.KindFile] != 5 {
		t.Errorf("expected 5 file events; got %d", summary.EventCounts[event.KindFile])
	}
}

// --- Trigger ---------------------------------------------------------------

func TestTrigger_sendsOnChannel(t *testing.T) {
	db := openMemoryStore(t)
	a := New(db, nil, time.Hour, newTestLogger())

	// Channel starts empty; Trigger should send one item.
	a.Trigger()

	select {
	case <-a.triggerCh:
		// Good — trigger was received.
	default:
		t.Error("Trigger() did not send on triggerCh")
	}
}

func TestTrigger_idempotent(t *testing.T) {
	db := openMemoryStore(t)
	a := New(db, nil, time.Hour, newTestLogger())

	// Calling Trigger twice should not block (channel is buffered at 1).
	a.Trigger()
	a.Trigger() // second call must be a no-op, not block

	// Exactly one item should be in the channel.
	count := 0
	for {
		select {
		case <-a.triggerCh:
			count++
		default:
			if count != 1 {
				t.Errorf("expected 1 item in triggerCh after 2 Trigger() calls; got %d", count)
			}
			return
		}
	}
}

// --- Run -------------------------------------------------------------------

func TestRun_cancelImmediately(t *testing.T) {
	db := openMemoryStore(t)
	a := New(db, nil, time.Hour, newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Run starts

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Run(ctx)
	}()

	select {
	case <-done:
		// Run returned after cancellation — correct.
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestRun_triggerPathExecutes(t *testing.T) {
	db := openMemoryStore(t)

	summaryCh := make(chan Summary, 10)
	a := New(db, nil, time.Hour, newTestLogger())
	a.OnSummary = func(s Summary) {
		select {
		case summaryCh <- s:
		default:
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Queue a trigger before Run starts so the trigger branch fires after
	// the initial runCycle.
	a.Trigger()

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Run(ctx)
	}()

	// Wait for at least 2 summaries (initial + triggered) or timeout.
	received := 0
	deadline := time.After(3 * time.Second)
loop:
	for {
		select {
		case <-summaryCh:
			received++
			if received >= 2 {
				break loop
			}
		case <-deadline:
			break loop
		}
	}

	cancel()
	<-done

	if received < 1 {
		t.Errorf("expected at least 1 summary from Run; got %d", received)
	}
}

// --- runCycle --------------------------------------------------------------

func TestRunCycle_noEngine_callsOnSummary(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	var called int
	a := New(db, nil, time.Hour, newTestLogger())
	a.OnSummary = func(s Summary) {
		called++
		if s.Period != time.Hour {
			t.Errorf("summary.Period = %v; want %v", s.Period, time.Hour)
		}
		if s.GeneratedAt.IsZero() {
			t.Error("summary.GeneratedAt is zero; want non-zero")
		}
		if s.EventCounts == nil {
			t.Error("summary.EventCounts is nil; want initialized map")
		}
	}

	a.runCycle(ctx)

	if called != 1 {
		t.Errorf("OnSummary called %d times; want 1", called)
	}
}

func TestRunCycle_nilOnSummary_noPanic(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	a := New(db, nil, time.Hour, newTestLogger())
	a.OnSummary = nil

	// Must not panic.
	a.runCycle(ctx)
}

func TestRunCycle_withEventsInWindow(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Insert events within the analysis window.
	for i := range 3 {
		if err := db.InsertEvent(ctx, event.Event{
			Kind:      event.KindFile,
			Source:    "test",
			Payload:   map[string]any{"path": "/proj/main.go"},
			Timestamp: now.Add(-time.Duration(i+1) * 10 * time.Minute),
		}); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	var got Summary
	a := New(db, nil, time.Hour, newTestLogger())
	a.OnSummary = func(s Summary) { got = s }
	a.runCycle(ctx)

	if got.EventCounts[event.KindFile] != 3 {
		t.Errorf("EventCounts[file] = %d; want 3", got.EventCounts[event.KindFile])
	}
}

func TestRunCycle_withEngine_pingSucceeds(t *testing.T) {
	ts := mockInferenceServer("Insightful analysis.")
	defer ts.Close()

	db := openMemoryStore(t)
	ctx := context.Background()

	engine := newTestEngine(t, ts.URL)
	var got Summary
	a := New(db, engine, time.Hour, newTestLogger())
	a.OnSummary = func(s Summary) { got = s }

	a.runCycle(ctx)

	if got.Insights == "" {
		t.Error("runCycle with working engine should populate Insights via cloudPass")
	}
}

func TestRunCycle_withEngine_pingFails(t *testing.T) {
	// Use a server that's already closed — Ping will fail.
	ts := mockInferenceServer("irrelevant")
	ts.Close() // close immediately

	db := openMemoryStore(t)
	ctx := context.Background()

	engine := newTestEngine(t, ts.URL)
	var got Summary
	a := New(db, engine, time.Hour, newTestLogger())
	a.OnSummary = func(s Summary) { got = s }

	// runCycle must not error even when Ping fails — it logs a warning and
	// continues without the cloud pass.
	a.runCycle(ctx)

	// Summary should still be produced (from localPass).
	if got.EventCounts == nil {
		t.Error("expected summary even when engine ping fails")
	}
	// Insights should be empty since cloudPass was skipped.
	if got.Insights != "" {
		t.Error("expected empty Insights when cloudPass is skipped")
	}
}

// --- cloudPass -------------------------------------------------------------

func TestCloudPass_successEnrichesInsights(t *testing.T) {
	ts := mockInferenceServer("Excellent workflow patterns detected.")
	defer ts.Close()

	db := openMemoryStore(t)
	ctx := context.Background()

	engine := newTestEngine(t, ts.URL)
	a := New(db, engine, time.Hour, newTestLogger())

	summary := Summary{
		Period:      time.Hour,
		EventCounts: map[event.Kind]int64{},
	}

	err := a.cloudPass(ctx, &summary)
	if err != nil {
		t.Fatalf("cloudPass: %v", err)
	}

	if summary.Insights == "" {
		t.Error("cloudPass did not populate Insights field")
	}
	if summary.InferenceRouting == "" {
		t.Error("cloudPass did not populate InferenceRouting field")
	}
}

func TestCloudPass_persistsAIInteraction(t *testing.T) {
	ts := mockInferenceServer("Analysis complete.")
	defer ts.Close()

	db := openMemoryStore(t)
	ctx := context.Background()

	engine := newTestEngine(t, ts.URL)
	a := New(db, engine, time.Hour, newTestLogger())

	summary := Summary{
		Period:      time.Hour,
		EventCounts: map[event.Kind]int64{},
	}

	if err := a.cloudPass(ctx, &summary); err != nil {
		t.Fatalf("cloudPass: %v", err)
	}

	// Verify the AI interaction was persisted by querying it back.
	interactions, err := db.QueryAIInteractions(ctx, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("QueryAIInteractions: %v", err)
	}
	if len(interactions) != 1 {
		t.Errorf("expected 1 persisted AI interaction; got %d", len(interactions))
	}
	if interactions[0].QueryCategory != "workflow_analysis" {
		t.Errorf("QueryCategory = %q; want workflow_analysis", interactions[0].QueryCategory)
	}
}
