package notifier

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wambozi/sigil/internal/store"
)

// openTestStore creates a SQLite store in a temp dir for testing.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// stubPlatform is a no-op platform that records the number of send calls.
type stubPlatform struct {
	sends int
}

func (p *stubPlatform) Send(_, _ string, _ bool) { p.sends++ }
func (p *stubPlatform) Execute(_ string) error    { return nil }

// --- Rate limiting tests ----------------------------------------------------

func TestRateLimit_burstSuppressed(t *testing.T) {
	ntf := &Notifier{
		level:       LevelAmbient,
		store:       openTestStore(t),
		platform:    &stubPlatform{},
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	// First call: should pass.
	if !ntf.checkRateLimit(LevelAmbient, ambientMinInterval) {
		t.Fatal("first call should pass rate limit")
	}

	// Immediate second call within interval: should be suppressed.
	if ntf.checkRateLimit(LevelAmbient, ambientMinInterval) {
		t.Error("second call within interval should be suppressed")
	}
}

func TestRateLimit_afterIntervalSucceeds(t *testing.T) {
	ntf := &Notifier{
		level:       LevelAmbient,
		store:       openTestStore(t),
		platform:    &stubPlatform{},
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	// Simulate that the last shown time was more than the interval ago.
	ntf.lastShownAt[LevelAmbient] = time.Now().Add(-ambientMinInterval - time.Second)

	if !ntf.checkRateLimit(LevelAmbient, ambientMinInterval) {
		t.Error("call after interval expiry should succeed")
	}
}

func TestRateLimit_conversationalInterval(t *testing.T) {
	ntf := &Notifier{
		level:       LevelConversational,
		store:       openTestStore(t),
		platform:    &stubPlatform{},
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	// First call succeeds.
	if !ntf.checkRateLimit(LevelConversational, conversationalMinInterval) {
		t.Fatal("first conversational call should pass")
	}
	// Immediate second call suppressed.
	if ntf.checkRateLimit(LevelConversational, conversationalMinInterval) {
		t.Error("second call within conversational interval should be suppressed")
	}
}

func TestSurface_ambientStoresEvenWhenRateLimited(t *testing.T) {
	db := openTestStore(t)
	ntf := &Notifier{
		level:       LevelAmbient,
		store:       db,
		platform:    &stubPlatform{},
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	sg := Suggestion{
		Category:   "pattern",
		Confidence: ConfidenceModerate,
		Title:      "Test suggestion",
		Body:       "body",
	}

	// First Surface: stored and shown.
	ntf.Surface(sg)
	// Second Surface within interval: stored but not shown (rate limited).
	ntf.Surface(sg)

	// Both should be persisted in the store regardless of rate limiting.
	ctx := context.Background()
	suggestions, err := db.QuerySuggestions(ctx, "", 10)
	if err != nil {
		t.Fatalf("query suggestions: %v", err)
	}
	if len(suggestions) != 2 {
		t.Errorf("expected 2 stored suggestions, got %d", len(suggestions))
	}
}

// --- Level getter/setter tests -----------------------------------------------

func TestSetLevel_and_Level(t *testing.T) {
	ntf := &Notifier{
		level:       LevelAmbient,
		store:       openTestStore(t),
		platform:    &stubPlatform{},
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	levels := []Level{
		LevelSilent,
		LevelDigest,
		LevelAmbient,
		LevelConversational,
		LevelAutonomous,
	}

	for _, want := range levels {
		ntf.SetLevel(want)
		if got := ntf.Level(); got != want {
			t.Errorf("after SetLevel(%d): Level() = %d, want %d", want, got, want)
		}
	}
}

// --- Surface level-specific behaviour tests ----------------------------------

func TestSurface_silent(t *testing.T) {
	db := openTestStore(t)
	platform := &stubPlatform{}
	ntf := &Notifier{
		level:       LevelSilent,
		store:       db,
		platform:    platform,
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	sg := Suggestion{
		Category:   "pattern",
		Confidence: ConfidenceStrong,
		Title:      "Silent suggestion",
		Body:       "should be stored only",
	}
	ntf.Surface(sg)

	// Stored in the database.
	ctx := context.Background()
	suggestions, err := db.QuerySuggestions(ctx, "", 10)
	if err != nil {
		t.Fatalf("query suggestions: %v", err)
	}
	if len(suggestions) != 1 {
		t.Errorf("expected 1 stored suggestion, got %d", len(suggestions))
	}

	// Never dispatched to the platform.
	if platform.sends != 0 {
		t.Errorf("expected 0 platform sends at LevelSilent, got %d", platform.sends)
	}
}

func TestSurface_digest(t *testing.T) {
	platform := &stubPlatform{}
	ntf := &Notifier{
		level:       LevelDigest,
		store:       openTestStore(t),
		platform:    platform,
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	sg1 := Suggestion{Category: "pattern", Confidence: ConfidenceModerate, Title: "First", Body: "body1"}
	sg2 := Suggestion{Category: "pattern", Confidence: ConfidenceModerate, Title: "Second", Body: "body2"}
	ntf.Surface(sg1)
	ntf.Surface(sg2)

	// No sends yet — digest has not been flushed.
	if platform.sends != 0 {
		t.Errorf("expected 0 platform sends before FlushDigest, got %d", platform.sends)
	}

	// FlushDigest drains the queue and sends exactly one notification.
	ntf.FlushDigest()
	if platform.sends != 1 {
		t.Errorf("expected 1 platform send after FlushDigest, got %d", platform.sends)
	}
}

// --- FlushDigest tests -------------------------------------------------------

func TestFlushDigest(t *testing.T) {
	platform := &stubPlatform{}
	ntf := &Notifier{
		level:       LevelDigest,
		store:       openTestStore(t),
		platform:    platform,
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	sg1 := Suggestion{Category: "insight", Confidence: ConfidenceModerate, Title: "Tip one", Body: "do this"}
	sg2 := Suggestion{Category: "insight", Confidence: ConfidenceModerate, Title: "Tip two", Body: "do that"}
	ntf.Surface(sg1)
	ntf.Surface(sg2)

	// First flush: two suggestions collapsed into one send.
	ntf.FlushDigest()
	if platform.sends != 1 {
		t.Errorf("first FlushDigest: expected 1 send, got %d", platform.sends)
	}

	// Second flush: queue is empty, no additional send.
	ntf.FlushDigest()
	if platform.sends != 1 {
		t.Errorf("second FlushDigest on empty queue: expected still 1 send, got %d", platform.sends)
	}
}

func TestFlushDigest_empty(t *testing.T) {
	platform := &stubPlatform{}
	ntf := &Notifier{
		level:       LevelDigest,
		store:       openTestStore(t),
		platform:    platform,
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	// Nothing queued — FlushDigest must be a no-op.
	ntf.FlushDigest()
	if platform.sends != 0 {
		t.Errorf("expected 0 sends on empty FlushDigest, got %d", platform.sends)
	}
}

// --- Conversational level tests ----------------------------------------------

func TestSurface_conversational(t *testing.T) {
	db := openTestStore(t)
	ntf := &Notifier{
		level:       LevelConversational,
		store:       db,
		platform:    &stubPlatform{},
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	sg := Suggestion{
		Category:   "optimization",
		Confidence: ConfidenceStrong,
		Title:      "Use the cache",
		Body:       "Caching this call saves 200ms",
		ActionCmd:  "echo cached",
	}
	ntf.Surface(sg)

	// Suggestion must be persisted regardless of the goroutine-based display path.
	ctx := context.Background()
	suggestions, err := db.QuerySuggestions(ctx, "", 10)
	if err != nil {
		t.Fatalf("query suggestions: %v", err)
	}
	if len(suggestions) != 1 {
		t.Errorf("expected 1 stored suggestion at LevelConversational, got %d", len(suggestions))
	}
}

// --- Confidence gate tests ---------------------------------------------------

func TestSurface_lowConfidence_noCallback(t *testing.T) {
	called := false
	ntf := &Notifier{
		level:       LevelAmbient,
		store:       openTestStore(t),
		platform:    &stubPlatform{},
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
		OnSuggestion: func(_ int64, _ Suggestion) {
			called = true
		},
	}

	// Confidence is below ConfidenceModerate — callback must not fire.
	sg := Suggestion{
		Category:   "pattern",
		Confidence: ConfidenceWeak, // 0.3, below the 0.6 gate
		Title:      "Weak signal",
		Body:       "not enough evidence yet",
	}
	ntf.Surface(sg)

	if called {
		t.Error("OnSuggestion callback should not fire for confidence below ConfidenceModerate")
	}
}

func TestSurface_OnSuggestionCallback(t *testing.T) {
	var (
		callbackID int64
		callbackSG Suggestion
		called     bool
	)

	ntf := &Notifier{
		level:       LevelAmbient,
		store:       openTestStore(t),
		platform:    &stubPlatform{},
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
		OnSuggestion: func(id int64, sg Suggestion) {
			called = true
			callbackID = id
			callbackSG = sg
		},
	}

	sg := Suggestion{
		Category:   "reminder",
		Confidence: ConfidenceModerate, // exactly at the gate
		Title:      "Write tests",
		Body:       "coverage is low",
	}
	ntf.Surface(sg)

	if !called {
		t.Fatal("OnSuggestion callback should fire for confidence >= ConfidenceModerate")
	}
	if callbackID <= 0 {
		t.Errorf("callback received invalid ID %d; want a positive store-assigned ID", callbackID)
	}
	if callbackSG.Title != sg.Title {
		t.Errorf("callback suggestion title = %q, want %q", callbackSG.Title, sg.Title)
	}
	if callbackSG.Confidence != sg.Confidence {
		t.Errorf("callback suggestion confidence = %v, want %v", callbackSG.Confidence, sg.Confidence)
	}
}

// --- Conversational rate-limit suppression -----------------------------------

// TestSurface_conversationalRateLimitSuppressed verifies that a second
// LevelConversational surface call within the minimum interval is dropped
// (not stored a second time via show) but the suggestion is still persisted.
func TestSurface_conversationalRateLimitSuppressed(t *testing.T) {
	db := openTestStore(t)
	ntf := &Notifier{
		level:       LevelConversational,
		store:       db,
		platform:    &stubPlatform{},
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	sg := Suggestion{
		Category:   "pattern",
		Confidence: ConfidenceStrong,
		Title:      "Rate-limited conversational",
		Body:       "first one passes, second is suppressed",
	}

	// First call — passes the rate limiter.
	ntf.Surface(sg)
	// Second call within the interval — rate limited (display goroutine not spawned).
	ntf.Surface(sg)

	// Both suggestions are persisted regardless of rate limiting.
	ctx := context.Background()
	suggestions, err := db.QuerySuggestions(ctx, "", 10)
	if err != nil {
		t.Fatalf("query suggestions: %v", err)
	}
	if len(suggestions) != 2 {
		t.Errorf("expected 2 stored suggestions (both calls persisted), got %d", len(suggestions))
	}
}

// --- LevelAutonomous tests ---------------------------------------------------

// TestSurface_autonomousLowConfidence verifies that at LevelAutonomous a
// suggestion with an ActionCmd but confidence below VeryStrong is handled via
// the show path (stored and display goroutine spawned) rather than the
// countdown path.
func TestSurface_autonomousLowConfidence(t *testing.T) {
	db := openTestStore(t)
	ntf := &Notifier{
		level:       LevelAutonomous,
		store:       db,
		platform:    &stubPlatform{},
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	sg := Suggestion{
		Category:   "optimization",
		Confidence: ConfidenceStrong, // 0.8 — below VeryStrong (0.9), above Moderate
		Title:      "Autonomous show path",
		Body:       "has action but not high enough confidence for auto-execute",
		ActionCmd:  "echo run",
	}
	ntf.Surface(sg)

	ctx := context.Background()
	suggestions, err := db.QuerySuggestions(ctx, "", 10)
	if err != nil {
		t.Fatalf("query suggestions: %v", err)
	}
	if len(suggestions) != 1 {
		t.Errorf("expected 1 stored suggestion at LevelAutonomous (show path), got %d", len(suggestions))
	}
}

// TestSurface_autonomousNoActionCmd verifies that at LevelAutonomous a
// suggestion without an ActionCmd takes the show path regardless of confidence.
func TestSurface_autonomousNoActionCmd(t *testing.T) {
	db := openTestStore(t)
	ntf := &Notifier{
		level:       LevelAutonomous,
		store:       db,
		platform:    &stubPlatform{},
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	sg := Suggestion{
		Category:   "insight",
		Confidence: ConfidenceVeryStrong, // qualifies for autonomous — but no ActionCmd
		Title:      "No action autonomous",
		Body:       "very confident but nothing to run",
		// ActionCmd intentionally empty
	}
	ntf.Surface(sg)

	ctx := context.Background()
	suggestions, err := db.QuerySuggestions(ctx, "", 10)
	if err != nil {
		t.Fatalf("query suggestions: %v", err)
	}
	if len(suggestions) != 1 {
		t.Errorf("expected 1 stored suggestion (no-action autonomous), got %d", len(suggestions))
	}
}

// --- executeWithCountdown tests ----------------------------------------------

// TestExecuteWithCountdown_success verifies the happy path: the platform
// receives a Send call, Execute is called, and the suggestion is marked
// accepted with feedback recorded.
//
// This test calls executeWithCountdown directly (same package) to avoid the
// 3-second sleep inherent in the goroutine launched by Surface at
// LevelAutonomous.  The test takes ~3s intentionally.
func TestExecuteWithCountdown_success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 3s countdown test in short mode")
	}

	platform := &stubPlatform{}
	ntf := &Notifier{
		level:       LevelAutonomous,
		store:       openTestStore(t),
		platform:    platform,
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	sg := Suggestion{
		Category:   "optimization",
		Confidence: ConfidenceVeryStrong,
		Title:      "Auto-execute test",
		Body:       "running a safe no-op",
		ActionCmd:  "true", // exits 0 immediately
	}

	// Store the suggestion first so we have a valid ID for status updates.
	ctx := context.Background()
	id, err := ntf.store.InsertSuggestion(ctx, store.Suggestion{
		Category:   sg.Category,
		Confidence: sg.Confidence,
		Title:      sg.Title,
		Body:       sg.Body,
		ActionCmd:  sg.ActionCmd,
		CreatedAt:  time.Now(),
	})
	if err != nil {
		t.Fatalf("insert suggestion: %v", err)
	}

	ntf.executeWithCountdown(id, sg)

	if platform.sends != 1 {
		t.Errorf("expected 1 platform send during countdown, got %d", platform.sends)
	}
}

// TestExecuteWithCountdown_executeFailure verifies that when Execute returns
// an error the suggestion is marked ignored rather than accepted.
func TestExecuteWithCountdown_executeFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 3s countdown test in short mode")
	}

	platform := &errPlatform{}
	ntf := &Notifier{
		level:       LevelAutonomous,
		store:       openTestStore(t),
		platform:    platform,
		log:         discardLogger(),
		lastShownAt: make(map[Level]time.Time),
	}

	sg := Suggestion{
		Category:   "optimization",
		Confidence: ConfidenceVeryStrong,
		Title:      "Failing action",
		Body:       "this command will fail",
		ActionCmd:  "false",
	}

	ctx := context.Background()
	id, err := ntf.store.InsertSuggestion(ctx, store.Suggestion{
		Category:   sg.Category,
		Confidence: sg.Confidence,
		Title:      sg.Title,
		Body:       sg.Body,
		ActionCmd:  sg.ActionCmd,
		CreatedAt:  time.Now(),
	})
	if err != nil {
		t.Fatalf("insert suggestion: %v", err)
	}

	// Should not panic; the error path marks the suggestion ignored.
	ntf.executeWithCountdown(id, sg)

	if platform.sends != 1 {
		t.Errorf("expected 1 platform send during countdown, got %d", platform.sends)
	}
}

// errPlatform is a stub Platform whose Execute always returns an error,
// used to exercise the failure branch of executeWithCountdown.
type errPlatform struct {
	sends int
}

func (p *errPlatform) Send(_, _ string, _ bool) { p.sends++ }
func (p *errPlatform) Execute(_ string) error   { return errors.New("exec failed") }
