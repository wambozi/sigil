package notifier

import (
	"context"
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

func (p *stubPlatform) send(_, _ string, _ bool) { p.sends++ }
func (p *stubPlatform) execute(_ string) error    { return nil }

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
