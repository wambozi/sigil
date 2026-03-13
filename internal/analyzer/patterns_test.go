package analyzer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/notifier"
	"github.com/wambozi/sigil/internal/store"
)

// insertFile inserts a file event with the given path and timestamp.
func insertFile(t *testing.T, ctx context.Context, db interface {
	InsertEvent(context.Context, event.Event) error
}, path string, ts time.Time) {
	t.Helper()
	if err := db.InsertEvent(ctx, event.Event{
		Kind:      event.KindFile,
		Source:    "test",
		Payload:   map[string]any{"path": path},
		Timestamp: ts,
	}); err != nil {
		t.Fatalf("insertFile %s: %v", path, err)
	}
}

// insertTerminal inserts a terminal event with the given command, exit code,
// working directory, and timestamp.
func insertTerminal(t *testing.T, ctx context.Context, db interface {
	InsertEvent(context.Context, event.Event) error
}, cmd string, exitCode int, cwd string, ts time.Time) {
	t.Helper()
	if err := db.InsertEvent(ctx, event.Event{
		Kind:   event.KindTerminal,
		Source: "test",
		Payload: map[string]any{
			"cmd":       cmd,
			"exit_code": float64(exitCode), // JSON numbers decode as float64
			"cwd":       cwd,
		},
		Timestamp: ts,
	}); err != nil {
		t.Fatalf("insertTerminal %q: %v", cmd, err)
	}
}

// insertTerminalSession inserts a terminal event with a session_id.
func insertTerminalSession(t *testing.T, ctx context.Context, db interface {
	InsertEvent(context.Context, event.Event) error
}, cmd string, exitCode int, cwd string, ts time.Time, sessionID string) {
	t.Helper()
	if err := db.InsertEvent(ctx, event.Event{
		Kind:   event.KindTerminal,
		Source: "test",
		Payload: map[string]any{
			"cmd":        cmd,
			"exit_code":  float64(exitCode),
			"cwd":        cwd,
			"session_id": sessionID,
		},
		Timestamp: ts,
	}); err != nil {
		t.Fatalf("insertTerminalSession %q: %v", cmd, err)
	}
}

// hasSuggestionWithTitle returns true if any suggestion in ss has the given
// title, and reports the full suggestion list on failure.
func hasSuggestionWithTitle(t *testing.T, ss []notifier.Suggestion, title string) bool {
	t.Helper()
	for _, s := range ss {
		if s.Title == title {
			return true
		}
	}
	return false
}

// --- EditThenTest -----------------------------------------------------------

func TestDetector_EditThenTest_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Insert 5 file edits in /home/user/project, each followed within
	// 2 minutes by a "go test" — ratio = 100 %, well above 60 % threshold.
	for i := range 5 {
		base := now.Add(-time.Duration(i+1) * 10 * time.Minute)
		insertFile(t, ctx, db, "/home/user/project/main.go", base)
		insertTerminal(t, ctx, db, "go test ./...", 0, "/home/user/project", base.Add(2*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, 2*time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Edit-then-test pattern detected") {
		t.Errorf("expected EditThenTest suggestion; got %+v", suggestions)
	}
}

func TestDetector_EditThenTest_belowThreshold_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// 5 file edits, only 1 followed by a test (20 % ratio — below 60 %).
	for i := range 5 {
		base := now.Add(-time.Duration(i+1) * 10 * time.Minute)
		insertFile(t, ctx, db, "/home/user/project/main.go", base)
	}
	insertTerminal(t, ctx, db, "go test ./...", 0, "/home/user/project",
		now.Add(-9*time.Minute+30*time.Second))

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, 2*time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "Edit-then-test pattern detected") {
		t.Error("expected no EditThenTest suggestion below threshold")
	}
}

// --- EditTestFailLoop -------------------------------------------------------

func TestDetector_EditTestFailLoop_threeCycles_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Simulate 3 edit→fail→re-edit cycles on the same file within 30 minutes.
	// Cycle 1: edit at -25min, test fails at -24min
	// Cycle 2: re-edit at -20min, test fails at -19min
	// Cycle 3: re-edit at -15min, test fails at -14min
	// Final re-edit at -10min (completes 3rd cycle)
	insertFile(t, ctx, db, "/proj/handler.go", now.Add(-25*time.Minute))
	insertTerminal(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-24*time.Minute))
	insertFile(t, ctx, db, "/proj/handler.go", now.Add(-20*time.Minute)) // cycle 1 complete
	insertTerminal(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-19*time.Minute))
	insertFile(t, ctx, db, "/proj/handler.go", now.Add(-15*time.Minute)) // cycle 2 complete
	insertTerminal(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-14*time.Minute))
	insertFile(t, ctx, db, "/proj/handler.go", now.Add(-10*time.Minute)) // cycle 3 complete

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Edit-test-fail loop detected") {
		t.Errorf("expected edit-test-fail loop suggestion; got %+v", suggestions)
	}
}

func TestDetector_EditTestFailLoop_belowThreshold_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Only 2 cycles — below the threshold of 3.
	insertFile(t, ctx, db, "/proj/handler.go", now.Add(-25*time.Minute))
	insertTerminal(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-24*time.Minute))
	insertFile(t, ctx, db, "/proj/handler.go", now.Add(-20*time.Minute)) // cycle 1
	insertTerminal(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-19*time.Minute))
	insertFile(t, ctx, db, "/proj/handler.go", now.Add(-15*time.Minute)) // cycle 2

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "Edit-test-fail loop detected") {
		t.Error("expected no suggestion with only 2 cycles")
	}
}

func TestDetector_EditTestFailLoop_successBreaksCycle(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Edit → fail → re-edit → SUCCESS → edit → fail → re-edit.
	// The success resets the pattern; only 1 cycle after the success.
	insertFile(t, ctx, db, "/proj/handler.go", now.Add(-25*time.Minute))
	insertTerminal(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-24*time.Minute))
	insertFile(t, ctx, db, "/proj/handler.go", now.Add(-20*time.Minute))              // cycle 1
	insertTerminal(t, ctx, db, "go test ./...", 0, "/proj", now.Add(-19*time.Minute)) // success
	insertFile(t, ctx, db, "/proj/handler.go", now.Add(-15*time.Minute))
	insertTerminal(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-14*time.Minute))
	insertFile(t, ctx, db, "/proj/handler.go", now.Add(-10*time.Minute)) // cycle 2

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "Edit-test-fail loop detected") {
		t.Error("expected no suggestion when success breaks the cycle")
	}
}

// --- StuckDetection ---------------------------------------------------------

func TestDetector_StuckDetection_manyEditsWithFailures_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// 6 edits to the same file in 10 minutes (above threshold of 5 in 15min),
	// with a test failure in the same window.
	for i := range 6 {
		insertFile(t, ctx, db, "/proj/handler.go", now.Add(-time.Duration(10-i)*time.Minute))
	}
	insertTerminal(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-5*time.Minute))

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Possible stuck on file") {
		t.Errorf("expected stuck detection suggestion; got %+v", suggestions)
	}
}

func TestDetector_StuckDetection_manyEditsNoFailures_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// 6 edits but all tests pass — not stuck, just active.
	for i := range 6 {
		insertFile(t, ctx, db, "/proj/handler.go", now.Add(-time.Duration(10-i)*time.Minute))
	}
	insertTerminal(t, ctx, db, "go test ./...", 0, "/proj", now.Add(-5*time.Minute))

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "Possible stuck on file") {
		t.Error("expected no stuck suggestion when tests pass")
	}
}

func TestDetector_StuckDetection_fewEditsWithFailures_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Only 3 edits (below threshold of 5) with a failure.
	for i := range 3 {
		insertFile(t, ctx, db, "/proj/handler.go", now.Add(-time.Duration(10-i)*time.Minute))
	}
	insertTerminal(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-5*time.Minute))

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "Possible stuck on file") {
		t.Error("expected no stuck suggestion with only 3 edits")
	}
}

// --- DependencyChurn --------------------------------------------------------

func TestDetector_DependencyChurn_fourEdits_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// 4 edits to go.sum (at or above threshold of 4).
	for i := range 4 {
		insertFile(t, ctx, db, "/proj/go.sum", now.Add(-time.Duration(4-i)*10*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, 2*time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Dependency churn detected") {
		t.Errorf("expected dependency churn suggestion; got %+v", suggestions)
	}
}

func TestDetector_DependencyChurn_belowThreshold_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Only 2 edits to package-lock.json — below threshold.
	for i := range 2 {
		insertFile(t, ctx, db, "/proj/package-lock.json", now.Add(-time.Duration(2-i)*10*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, 2*time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "Dependency churn detected") {
		t.Error("expected no dependency churn suggestion below threshold")
	}
}

func TestDetector_DependencyChurn_nonDepFile_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// 10 edits to a regular file — not a dep file.
	for i := range 10 {
		insertFile(t, ctx, db, "/proj/main.go", now.Add(-time.Duration(10-i)*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, 2*time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "Dependency churn detected") {
		t.Error("expected no dependency churn suggestion for non-dep files")
	}
}

// --- BuildFailureStreak -----------------------------------------------------

func TestDetector_BuildFailureStreak_threeFailures_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Three consecutive "go test" failures.
	for i := range 3 {
		insertTerminal(t, ctx, db, "go test ./...", 1, "/home/user/project",
			now.Add(-time.Duration(3-i)*5*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "3 consecutive build/test failures") {
		t.Errorf("expected build failure streak suggestion; got %+v", suggestions)
	}
}

func TestDetector_BuildFailureStreak_twoFailuresThenSuccess_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Two failures, then a success resets the streak.
	insertTerminal(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-15*time.Minute))
	insertTerminal(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-10*time.Minute))
	insertTerminal(t, ctx, db, "go test ./...", 0, "/proj", now.Add(-5*time.Minute))

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "3 consecutive build/test failures") {
		t.Error("expected no streak suggestion after streak was broken by success")
	}
}

// --- BuildFailureStreak session-aware ---------------------------------------

func TestDetector_BuildFailureStreak_perSessionStreaks(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Session "A" has 3 consecutive failures (triggers suggestion).
	// Session "B" has 2 failures then a success (no streak).
	for i := range 3 {
		insertTerminalSession(t, ctx, db, "go test ./...", 1, "/proj",
			now.Add(-time.Duration(3-i)*5*time.Minute), "A")
	}
	insertTerminalSession(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-15*time.Minute), "B")
	insertTerminalSession(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-10*time.Minute), "B")
	insertTerminalSession(t, ctx, db, "go test ./...", 0, "/proj", now.Add(-5*time.Minute), "B")

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkBuildFailureStreak(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkBuildFailureStreak: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "3 consecutive build/test failures") {
		t.Errorf("expected build failure streak from session A; got %+v", suggestions)
	}
}

func TestDetector_BuildFailureStreak_crossSessionNotCounted(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// 2 failures in session A, 1 failure in session B — no 3-streak in either.
	insertTerminalSession(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-15*time.Minute), "A")
	insertTerminalSession(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-10*time.Minute), "A")
	insertTerminalSession(t, ctx, db, "go test ./...", 1, "/proj", now.Add(-5*time.Minute), "B")

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkBuildFailureStreak(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkBuildFailureStreak: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "3 consecutive build/test failures") {
		t.Error("expected no streak suggestion when failures span different sessions")
	}
}

// --- SessionLength session-aware --------------------------------------------

func TestDetector_SessionLength_sessionIDGrouping(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	// Create 3 sessions via session_id, each ~90 minutes long.
	// All events interleaved in time but with distinct session IDs.
	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	for session := range 3 {
		sid := fmt.Sprintf("sess_%d", session)
		sessionStart := base.Add(time.Duration(session) * 6 * time.Hour)
		for i := range 10 {
			insertTerminalSession(t, ctx, db, "vim main.go", 0, "/proj",
				sessionStart.Add(time.Duration(i)*9*time.Minute), sid)
		}
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkSessionLength(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkSessionLength: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Long coding sessions") {
		t.Errorf("expected session length suggestion with session IDs; got %+v", suggestions)
	}
}

// --- IdleGaps session-aware -------------------------------------------------

func TestDetector_IdleGaps_sessionIDGrouping(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	// Create 3 sessions via session_id.
	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	for session := range 3 {
		sid := fmt.Sprintf("sess_%d", session)
		sessionStart := base.Add(time.Duration(session) * 2 * time.Hour)
		for i := range 5 {
			insertTerminalSession(t, ctx, db, "vim main.go", 0, "/proj",
				sessionStart.Add(time.Duration(i)*5*time.Minute), sid)
		}
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkIdleGaps(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkIdleGaps: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Work session summary") {
		t.Errorf("expected idle gaps suggestion with session IDs; got %+v", suggestions)
	}
}

// --- ContextSwitchFrequency session-aware -----------------------------------

func TestDetector_ContextSwitchFrequency_perSessionSwitches(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	anchor := time.Now().Truncate(time.Hour).Add(30 * time.Minute)

	// Session "A" has 8 distinct directory changes within one hour (above limit).
	dirs := []string{"/a", "/b", "/c", "/d", "/e", "/f", "/g", "/h", "/i"}
	for i, dir := range dirs {
		insertTerminalSession(t, ctx, db, "ls", 0, dir,
			anchor.Add(time.Duration(i)*time.Minute), "A")
	}
	// Session "B" has only 2 switches (below limit).
	insertTerminalSession(t, ctx, db, "ls", 0, "/x", anchor, "B")
	insertTerminalSession(t, ctx, db, "ls", 0, "/y", anchor.Add(time.Minute), "B")

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkContextSwitchFrequency(ctx, anchor.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkContextSwitchFrequency: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "High context-switching") {
		t.Errorf("expected context-switch suggestion from session A; got %+v", suggestions)
	}
}

// --- Empty store ------------------------------------------------------------

func TestDetector_EmptyStore_noSuggestionsNoError(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, time.Hour)
	if err != nil {
		t.Fatalf("Detect on empty store: %v", err)
	}
	if len(suggestions) != 0 {
		t.Errorf("expected no suggestions from empty store; got %+v", suggestions)
	}
}

// --- ContextSwitchFrequency -------------------------------------------------

func TestDetector_ContextSwitchFrequency_aboveLimit_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	// Anchor events to the middle of the current clock hour so they are
	// guaranteed to fall within a single hour bucket regardless of when the
	// test runs.
	anchor := time.Now().Truncate(time.Hour).Add(30 * time.Minute)

	// Generate 8 distinct directory changes within the same hour.
	dirs := []string{"/a", "/b", "/c", "/d", "/e", "/f", "/g", "/h", "/i"}
	for i, dir := range dirs {
		insertTerminal(t, ctx, db, "ls", 0, dir,
			anchor.Add(time.Duration(i)*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, 2*time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "High context-switching") {
		t.Errorf("expected context-switch suggestion; got %+v", suggestions)
	}
}

// --- FrequentFiles ----------------------------------------------------------

func TestDetector_FrequentFiles_newFileInTopFive_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Yesterday: top-5 are a.go through e.go.
	for _, name := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		for i := range 3 {
			insertFile(t, ctx, db,
				"/proj/"+name,
				now.Add(-36*time.Hour+time.Duration(i)*time.Minute))
		}
	}

	// Today: handler.go rockets to the top with 10 edits (wasn't there yesterday).
	for i := range 10 {
		insertFile(t, ctx, db, "/proj/handler.go",
			now.Add(-time.Duration(i+1)*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, 48*time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Unusual file focus") {
		t.Errorf("expected unusual file focus suggestion; got %+v", suggestions)
	}
}

// --- TimeOfDay --------------------------------------------------------------

func TestDetector_TimeOfDay_peakHour_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	// Create a concentrated cluster of 10 file edits, all at the same hour
	// today, far enough in the past to remain within a 24-hour window.
	base := time.Now().Truncate(time.Hour).Add(-2 * time.Hour) // two hours ago, on the hour
	for i := range 10 {
		insertFile(t, ctx, db, "/proj/main.go",
			base.Add(time.Duration(i)*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.Detect(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Productive hour identified") {
		t.Errorf("expected time-of-day suggestion; got %+v", suggestions)
	}
}

// --- DayOfWeekProductivity --------------------------------------------------

func TestDetector_DayOfWeekProductivity_peakDay_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	// Use fixed dates: Monday gets 12 edits, Tuesday gets 5.
	// Peak (12) >= 2x trough (5) and peak >= 10.
	monday := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)  // Monday
	tuesday := time.Date(2026, 3, 3, 10, 0, 0, 0, time.UTC) // Tuesday

	for i := range 12 {
		insertFile(t, ctx, db, "/proj/main.go", monday.Add(time.Duration(i)*time.Minute))
	}
	for i := range 5 {
		insertFile(t, ctx, db, "/proj/main.go", tuesday.Add(time.Duration(i)*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkDayOfWeekProductivity(ctx, monday.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkDayOfWeekProductivity: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Day-of-week productivity pattern") {
		t.Errorf("expected day-of-week suggestion; got %+v", suggestions)
	}
}

func TestDetector_DayOfWeekProductivity_belowThreshold_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	// Both days have similar counts — no 2x difference.
	monday := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	tuesday := time.Date(2026, 3, 3, 10, 0, 0, 0, time.UTC)

	for i := range 6 {
		insertFile(t, ctx, db, "/proj/main.go", monday.Add(time.Duration(i)*time.Minute))
	}
	for i := range 5 {
		insertFile(t, ctx, db, "/proj/main.go", tuesday.Add(time.Duration(i)*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkDayOfWeekProductivity(ctx, monday.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkDayOfWeekProductivity: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "Day-of-week productivity pattern") {
		t.Error("expected no day-of-week suggestion when peak is not 2x trough")
	}
}

// --- SessionLength ----------------------------------------------------------

func TestDetector_SessionLength_longSessions_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	// Create 3 sessions of ~90 minutes each, separated by 3-hour gaps.
	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	for session := 0; session < 3; session++ {
		sessionStart := base.Add(time.Duration(session) * 6 * time.Hour)
		for i := 0; i < 10; i++ {
			insertTerminal(t, ctx, db, "vim main.go", 0, "/proj",
				sessionStart.Add(time.Duration(i)*9*time.Minute))
		}
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkSessionLength(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkSessionLength: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Long coding sessions") {
		t.Errorf("expected session length suggestion; got %+v", suggestions)
	}
}

func TestDetector_SessionLength_shortSessions_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	// Create 3 sessions of ~30 minutes each.
	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	for session := 0; session < 3; session++ {
		sessionStart := base.Add(time.Duration(session) * 4 * time.Hour)
		for i := 0; i < 4; i++ {
			insertTerminal(t, ctx, db, "vim main.go", 0, "/proj",
				sessionStart.Add(time.Duration(i)*10*time.Minute))
		}
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkSessionLength(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkSessionLength: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "Long coding sessions") {
		t.Error("expected no session length suggestion for short sessions")
	}
}

// --- IdleGaps ---------------------------------------------------------------

func TestDetector_IdleGaps_multipleSessions_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	// Create 3 sessions separated by 1-hour gaps (well above 30min sessionGap).
	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	for session := range 3 {
		sessionStart := base.Add(time.Duration(session) * 2 * time.Hour)
		for i := range 5 {
			insertTerminal(t, ctx, db, "vim main.go", 0, "/proj",
				sessionStart.Add(time.Duration(i)*5*time.Minute))
		}
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkIdleGaps(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkIdleGaps: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Work session summary") {
		t.Errorf("expected idle gaps suggestion; got %+v", suggestions)
	}
}

func TestDetector_IdleGaps_singleSession_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	// All events within one session (no gaps > 30min).
	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	for i := range 10 {
		insertTerminal(t, ctx, db, "vim main.go", 0, "/proj",
			base.Add(time.Duration(i)*2*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkIdleGaps(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkIdleGaps: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "Work session summary") {
		t.Error("expected no idle gaps suggestion for single session")
	}
}

// --- AIQueryCategoryTrends --------------------------------------------------

func insertAIInteraction(t *testing.T, ctx context.Context, db interface {
	InsertAIInteraction(context.Context, event.AIInteraction) error
}, category string, ts time.Time) {
	t.Helper()
	if err := db.InsertAIInteraction(ctx, event.AIInteraction{
		QueryText:     "test query",
		QueryCategory: category,
		Routing:       "local",
		LatencyMS:     100,
		Timestamp:     ts,
	}); err != nil {
		t.Fatalf("insertAIInteraction %s: %v", category, err)
	}
}

func TestDetector_AIQueryCategoryTrends_dominantCategory_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// 4 "debug" queries + 1 "code_gen" = 5 total, debug is 80%.
	for i := range 4 {
		insertAIInteraction(t, ctx, db, "debug", now.Add(-time.Duration(i+1)*time.Minute))
	}
	insertAIInteraction(t, ctx, db, "code_gen", now.Add(-6*time.Minute))

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkAIQueryCategoryTrends(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkAIQueryCategoryTrends: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "AI query category trend") {
		t.Errorf("expected AI category trend suggestion; got %+v", suggestions)
	}
}

func TestDetector_AIQueryCategoryTrends_noCategory_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Only 3 interactions — below the 5 minimum.
	for i := range 3 {
		insertAIInteraction(t, ctx, db, "debug", now.Add(-time.Duration(i+1)*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkAIQueryCategoryTrends(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkAIQueryCategoryTrends: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "AI query category trend") {
		t.Error("expected no suggestion with < 5 interactions")
	}
}

// --- SuggestionAcceptanceTrend ----------------------------------------------

func TestDetector_SuggestionAcceptanceTrend_highRate_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Insert 8 accepted and 2 dismissed suggestions (80% acceptance).
	for i := range 8 {
		id, err := db.InsertSuggestion(ctx, store.Suggestion{
			Category: "pattern", Confidence: 0.7, Title: "test", Body: "test",
			CreatedAt: now.Add(-time.Duration(i+1) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = db.UpdateSuggestionStatus(ctx, id, store.StatusAccepted)
	}
	for i := range 2 {
		id, err := db.InsertSuggestion(ctx, store.Suggestion{
			Category: "pattern", Confidence: 0.7, Title: "test", Body: "test",
			CreatedAt: now.Add(-time.Duration(i+9) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = db.UpdateSuggestionStatus(ctx, id, store.StatusDismissed)
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkSuggestionAcceptanceTrend(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkSuggestionAcceptanceTrend: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "High suggestion acceptance") {
		t.Errorf("expected high acceptance suggestion; got %+v", suggestions)
	}
}

func TestDetector_SuggestionAcceptanceTrend_lowRate_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// Insert 2 accepted and 9 dismissed (18% acceptance, 11 resolved >= 10).
	for i := range 2 {
		id, err := db.InsertSuggestion(ctx, store.Suggestion{
			Category: "pattern", Confidence: 0.7, Title: "test", Body: "test",
			CreatedAt: now.Add(-time.Duration(i+1) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = db.UpdateSuggestionStatus(ctx, id, store.StatusAccepted)
	}
	for i := range 9 {
		id, err := db.InsertSuggestion(ctx, store.Suggestion{
			Category: "pattern", Confidence: 0.7, Title: "test", Body: "test",
			CreatedAt: now.Add(-time.Duration(i+3) * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = db.UpdateSuggestionStatus(ctx, id, store.StatusDismissed)
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkSuggestionAcceptanceTrend(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkSuggestionAcceptanceTrend: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Low suggestion acceptance") {
		t.Errorf("expected low acceptance suggestion; got %+v", suggestions)
	}
}

// --- ProgressiveDisclosure ---------------------------------------------------

func TestDetectAITier(t *testing.T) {
	tests := []struct {
		name  string
		count int
		want  AITier
	}{
		{"zero", 0, TierObserver},
		{"few", 3, TierExplorer},
		{"moderate", 10, TierIntegrator},
		{"many", 25, TierNative},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interactions := make([]event.AIInteraction, tt.count)
			got := detectAITier(interactions)
			if got != tt.want {
				t.Errorf("detectAITier(%d interactions) = %d, want %d", tt.count, got, tt.want)
			}
		})
	}
}

func TestDetector_ProgressiveDisclosure_tier0_buildFailures(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// No AI interactions (tier 0) + 3 build failures → should suggest trying AI.
	for i := range 3 {
		insertTerminal(t, ctx, db, "go test ./...", 1, "/proj",
			now.Add(-time.Duration(3-i)*5*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkProgressiveDisclosure(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkProgressiveDisclosure: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Try the AI assistant") {
		t.Errorf("expected tier 0 AI discovery suggestion; got %+v", suggestions)
	}
}

func TestDetector_ProgressiveDisclosure_tier3_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// 25 AI interactions (tier 3) → no disclosure suggestion.
	for i := range 25 {
		insertAIInteraction(t, ctx, db, "debug", now.Add(-time.Duration(i+1)*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkProgressiveDisclosure(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkProgressiveDisclosure: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "Try the AI assistant") ||
		hasSuggestionWithTitle(t, suggestions, "Automate your test workflow") ||
		hasSuggestionWithTitle(t, suggestions, "Deep-dive with AI") {
		t.Errorf("expected no disclosure suggestion for tier 3; got %+v", suggestions)
	}
}

func TestDetector_ProgressiveDisclosure_tier2_fileAware(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()
	now := time.Now()

	// 10 AI interactions (tier 2) + file edits → codebase-aware suggestion.
	for i := range 10 {
		insertAIInteraction(t, ctx, db, "debug", now.Add(-time.Duration(i+1)*time.Minute))
	}
	for i := range 5 {
		insertFile(t, ctx, db, "/proj/handler.go", now.Add(-time.Duration(i+1)*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkProgressiveDisclosure(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkProgressiveDisclosure: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "Deep-dive with AI") {
		t.Errorf("expected tier 2 codebase-aware suggestion; got %+v", suggestions)
	}
}

// --- WindowContextSwitching -------------------------------------------------

func insertHyprland(t *testing.T, ctx context.Context, db interface {
	InsertEvent(context.Context, event.Event) error
}, windowClass, windowTitle string, ts time.Time) {
	t.Helper()
	if err := db.InsertEvent(ctx, event.Event{
		Kind:   event.KindHyprland,
		Source: "test",
		Payload: map[string]any{
			"window_class": windowClass,
			"window_title": windowTitle,
			"action":       "focus",
		},
		Timestamp: ts,
	}); err != nil {
		t.Fatalf("insertHyprland %s: %v", windowClass, err)
	}
}

func TestDetector_WindowContextSwitching_highRate_suggestionReturned(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	// 35 window focus events in a single hour — above the 30/hr threshold.
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	apps := []string{"kitty", "firefox", "code", "slack"}
	for i := range 35 {
		insertHyprland(t, ctx, db, apps[i%len(apps)], "title",
			base.Add(time.Duration(i)*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkWindowContextSwitching(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkWindowContextSwitching: %v", err)
	}

	if !hasSuggestionWithTitle(t, suggestions, "High window switching") {
		t.Errorf("expected window switching suggestion; got %+v", suggestions)
	}
}

func TestDetector_WindowContextSwitching_lowRate_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	// 10 focus events in an hour — below threshold.
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	for i := range 10 {
		insertHyprland(t, ctx, db, "kitty", "title",
			base.Add(time.Duration(i)*5*time.Minute))
	}

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkWindowContextSwitching(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkWindowContextSwitching: %v", err)
	}

	if hasSuggestionWithTitle(t, suggestions, "High window switching") {
		t.Error("expected no suggestion for low window switch rate")
	}
}

func TestDetector_WindowContextSwitching_noEvents_noSuggestion(t *testing.T) {
	db := openMemoryStore(t)
	ctx := context.Background()

	det := NewDetector(db, newTestLogger())
	suggestions, err := det.checkWindowContextSwitching(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("checkWindowContextSwitching: %v", err)
	}

	if len(suggestions) != 0 {
		t.Errorf("expected no suggestions with no events; got %+v", suggestions)
	}
}

// --- groupBySession ---------------------------------------------------------

func TestGroupBySession(t *testing.T) {
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	mkEvent := func(sid string, offset time.Duration) event.Event {
		payload := map[string]any{"cmd": "ls", "exit_code": float64(0), "cwd": "/proj"}
		if sid != "" {
			payload["session_id"] = sid
		}
		return event.Event{
			Kind:      event.KindTerminal,
			Source:    "test",
			Payload:   payload,
			Timestamp: base.Add(offset),
		}
	}

	tests := []struct {
		name       string
		events     []event.Event
		wantGroups int
	}{
		{
			name:       "empty_input",
			events:     nil,
			wantGroups: 0,
		},
		{
			name: "all_events_have_session_id",
			events: []event.Event{
				mkEvent("100", 0),
				mkEvent("100", time.Minute),
				mkEvent("200", 2*time.Minute),
				mkEvent("200", 3*time.Minute),
			},
			wantGroups: 2,
		},
		{
			name: "no_events_have_session_id",
			events: []event.Event{
				mkEvent("", 0),
				mkEvent("", time.Minute),
				mkEvent("", time.Hour), // gap > 30min
				mkEvent("", time.Hour+time.Minute),
			},
			wantGroups: 2,
		},
		{
			name: "mixed_events",
			events: []event.Event{
				mkEvent("100", 0),
				mkEvent("", 5*time.Minute),
				mkEvent("100", 10*time.Minute),
				mkEvent("", time.Hour), // gap > 30min from first no-session event
			},
			wantGroups: 3, // "100", "_ts_0", "_ts_1"
		},
		{
			name: "single_session",
			events: []event.Event{
				mkEvent("42", 0),
				mkEvent("42", time.Minute),
				mkEvent("42", 2*time.Minute),
			},
			wantGroups: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groups := groupBySession(tt.events)
			if len(groups) != tt.wantGroups {
				t.Errorf("groupBySession() returned %d groups, want %d; groups=%v",
					len(groups), tt.wantGroups, groupKeys(groups))
			}
			// Verify all events are accounted for.
			total := 0
			for _, g := range groups {
				total += len(g)
			}
			if total != len(tt.events) {
				t.Errorf("total events in groups = %d, want %d", total, len(tt.events))
			}
		})
	}
}

func groupKeys(groups map[string][]event.Event) []string {
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	return keys
}

// --- isTestOrBuildCmd -------------------------------------------------------

func TestIsTestOrBuildCmd(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go test ./...", true},
		{"go build .", true},
		{"go vet ./...", true},
		{"make all", true},
		{"cargo test", true},
		{"cargo build --release", true},
		{"npm test", true},
		{"npm run test", true},
		{"npm run build", true},
		{"pytest -v", true},
		{"python -m pytest tests/", true},
		{"./gradlew test", true},
		{"mvn test", true},
		{"git commit -m 'fix'", false},
		{"ls -la", false},
		{"echo hello", false},
		{"", false},
	}
	for _, tt := range tests {
		got := event.IsTestOrBuildCmd(tt.cmd)
		if got != tt.want {
			t.Errorf("isTestOrBuildCmd(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}
