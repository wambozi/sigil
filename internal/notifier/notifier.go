// Package notifier surfaces analyzer suggestions to the user at a configured
// aggression level.  All suggestions are persisted to the store regardless of
// level so they are always queryable via sigilctl.
//
// Five levels (matching the product plan):
//
//	0 – Silent:         store only, never displayed
//	1 – Digest:         one daily summary notification
//	2 – Ambient:        real-time, auto-dismissing toasts (default)
//	3 – Conversational: toasts with an action button
//	4 – Autonomous:     auto-execute high-confidence actions with countdown
package notifier

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wambozi/sigil/internal/store"
)

// SuggestionStore is the subset of store operations the notifier needs.
type SuggestionStore interface {
	InsertSuggestion(ctx context.Context, sg store.Suggestion) (int64, error)
	UpdateSuggestionStatus(ctx context.Context, id int64, status store.SuggestionStatus) error
	InsertFeedback(ctx context.Context, suggestionID int64, outcome string) error
}

// Level controls how aggressively suggestions are surfaced.
type Level int

const (
	LevelSilent         Level = 0
	LevelDigest         Level = 1
	LevelAmbient        Level = 2 // default
	LevelConversational Level = 3
	LevelAutonomous     Level = 4
)

// Confidence thresholds — suggestions below the level's minimum are stored but
// not displayed until enough observations have accumulated.
const (
	ConfidenceWeak       = 0.3 // 2-3 observations
	ConfidenceModerate   = 0.6 // 5-10 observations — minimum for LevelAmbient
	ConfidenceStrong     = 0.8 // 15+ observations
	ConfidenceVeryStrong = 0.9 // 25+ — eligible for LevelAutonomous auto-execute
)

// Rate-limiting constants — prevent notification floods when the analyzer
// produces a burst of suggestions.
const (
	ambientMinInterval        = 15 * time.Minute
	conversationalMinInterval = 5 * time.Minute
)

// Suggestion is a single insight ready to be surfaced.  It mirrors
// store.Suggestion but lives here to decouple callers from the store package.
type Suggestion struct {
	Category   string  // "pattern", "reminder", "optimization", "insight"
	Confidence float64 // 0.0–1.0
	Title      string  // Short headline (≤60 chars)
	Body       string  // One-sentence detail
	ActionCmd  string  // Optional shell command (empty if not actionable)
}

// Platform is the interface each OS backend implements.
type Platform interface {
	Send(title, body string, withAction bool)
	Execute(cmd string) error
}

// Notifier stores every suggestion and surfaces it according to the current
// Level.
type Notifier struct {
	mu       sync.RWMutex
	level    Level
	store    SuggestionStore
	platform Platform
	log      *slog.Logger

	// digestQueue accumulates suggestions for the daily digest (Level 1).
	digestQueue []Suggestion

	// lastShownAt tracks the last time a notification was displayed at each
	// level for rate-limiting purposes.
	lastShownAt map[Level]time.Time

	// recentSuggestions tracks title+body of recently surfaced suggestions
	// to avoid duplicates across analysis cycles.
	recentSuggestions map[string]time.Time

	// OnSuggestion, if set, is called after every suggestion that passes the
	// confidence gate (>= ConfidenceModerate). It receives the store-assigned
	// ID and the suggestion. Must be non-blocking.
	OnSuggestion func(id int64, sg Suggestion)

	// HasExternalSurface, if set, reports whether an external notification
	// surface (e.g. IDE extension) is actively connected. When true, desktop
	// notifications via Platform.Send are suppressed to avoid duplicates.
	HasExternalSurface func() bool
}

// New creates a Notifier at the given level.
func New(s SuggestionStore, level Level, log *slog.Logger) *Notifier {
	return &Notifier{
		level:            level,
		store:            s,
		platform:         newPlatform(log),
		log:              log,
		lastShownAt:      make(map[Level]time.Time),
		recentSuggestions: make(map[string]time.Time),
	}
}

// Level returns the current notification level.
func (n *Notifier) Level() Level {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.level
}

// SetLevel changes the notification level at runtime.
func (n *Notifier) SetLevel(l Level) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.level = l
	n.log.Info("notifier: level changed", "level", l)
}

// Surface persists the suggestion and displays it according to the current
// level.  Safe to call from any goroutine; storage and display are
// non-blocking.
// dedupWindow is how long a suggestion with the same title+body is suppressed.
const dedupWindow = 6 * time.Hour

func (n *Notifier) Surface(sg Suggestion) {
	ctx := context.Background()

	// Dedup: skip if the same title+body was surfaced recently.
	dedupKey := sg.Title + "|" + sg.Body
	n.mu.Lock()
	if last, ok := n.recentSuggestions[dedupKey]; ok && time.Since(last) < dedupWindow {
		n.mu.Unlock()
		return
	}
	n.recentSuggestions[dedupKey] = time.Now()
	// Prune old entries to prevent unbounded growth.
	for k, t := range n.recentSuggestions {
		if time.Since(t) > dedupWindow {
			delete(n.recentSuggestions, k)
		}
	}
	n.mu.Unlock()

	// Always persist — every suggestion is queryable via sigilctl regardless
	// of whether it was ever shown.
	id, err := n.store.InsertSuggestion(ctx, store.Suggestion{
		Category:   sg.Category,
		Confidence: sg.Confidence,
		Title:      sg.Title,
		Body:       sg.Body,
		ActionCmd:  sg.ActionCmd,
		CreatedAt:  time.Now(),
	})
	if err != nil {
		n.log.Warn("notifier: persist suggestion", "err", err)
		// Non-fatal — still attempt to display if possible.
	}

	// Fire the push callback for suggestions that pass the confidence gate.
	if err == nil && sg.Confidence >= ConfidenceModerate && n.OnSuggestion != nil {
		n.OnSuggestion(id, sg)
	}

	n.mu.RLock()
	level := n.level
	n.mu.RUnlock()

	// When an external surface (e.g. IDE extension) is connected, skip
	// desktop notifications — the suggestion was already pushed via
	// OnSuggestion above.
	externalActive := n.HasExternalSurface != nil && n.HasExternalSurface()

	switch level {
	case LevelSilent:
		// Stored above; nothing more to do.

	case LevelDigest:
		if !externalActive {
			n.mu.Lock()
			n.digestQueue = append(n.digestQueue, sg)
			n.mu.Unlock()
		}

	case LevelAmbient:
		if externalActive {
			return
		}
		if sg.Confidence < ConfidenceModerate {
			return // not confident enough to interrupt
		}
		if !n.checkRateLimit(LevelAmbient, ambientMinInterval) {
			n.log.Debug("notifier: ambient rate limit suppressed notification",
				"title", sg.Title)
			return
		}
		go n.show(id, sg, false)

	case LevelConversational:
		if externalActive {
			return
		}
		if !n.checkRateLimit(LevelConversational, conversationalMinInterval) {
			n.log.Debug("notifier: conversational rate limit suppressed notification",
				"title", sg.Title)
			return
		}
		go n.show(id, sg, sg.ActionCmd != "")

	case LevelAutonomous:
		if externalActive {
			return
		}
		if sg.ActionCmd != "" && sg.Confidence >= ConfidenceVeryStrong {
			go n.executeWithCountdown(id, sg)
		} else {
			go n.show(id, sg, sg.ActionCmd != "")
		}
	}
}

// checkRateLimit returns true if enough time has passed since the last shown
// notification at this level, and records the current time if so.
// Thread-safe: acquires the write lock internally.
func (n *Notifier) checkRateLimit(level Level, minInterval time.Duration) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	last := n.lastShownAt[level]
	if !last.IsZero() && time.Since(last) < minInterval {
		return false
	}
	n.lastShownAt[level] = time.Now()
	return true
}

// FlushDigest surfaces all queued digest suggestions as a single notification.
// Call this on a daily schedule (e.g. at 09:00) when level == LevelDigest.
func (n *Notifier) FlushDigest() {
	n.mu.Lock()
	queue := n.digestQueue
	n.digestQueue = nil
	n.mu.Unlock()

	if len(queue) == 0 {
		return
	}

	// Combine all queued suggestions into one digest notification.
	body := ""
	for i, sg := range queue {
		if i > 0 {
			body += "\n"
		}
		body += "• " + sg.Title + ": " + sg.Body
	}

	n.platform.Send("Sigil daily digest", body, false)
}

// show marks the suggestion as shown, sends the notification, then marks it
// ignored if the user doesn't interact (notification auto-dismisses).
func (n *Notifier) show(id int64, sg Suggestion, withAction bool) {
	ctx := context.Background()
	_ = n.store.UpdateSuggestionStatus(ctx, id, store.StatusShown)

	n.platform.Send(sg.Title, sg.Body, withAction)

	// For v0, all shown suggestions that aren't explicitly acted on are marked
	// ignored after a short window.  Phase 3 will hook into D-Bus action
	// callbacks for proper accept/dismiss tracking.
	time.Sleep(30 * time.Second)
	_ = n.store.UpdateSuggestionStatus(ctx, id, store.StatusIgnored)
}

// executeWithCountdown announces the action, waits 3 seconds for cancellation,
// then executes it.  For v0 the cancel window is advisory — there's no terminal
// UI to receive input, so it just logs.
func (n *Notifier) executeWithCountdown(id int64, sg Suggestion) {
	ctx := context.Background()
	n.log.Info("notifier: autonomous action in 3s",
		"cmd", sg.ActionCmd, "title", sg.Title)

	n.platform.Send(
		sg.Title,
		sg.Body+"\n[Running in 3s: "+sg.ActionCmd+"]",
		false,
	)

	time.Sleep(3 * time.Second)

	n.log.Info("notifier: executing autonomous action", "cmd", sg.ActionCmd)
	if err := n.platform.Execute(sg.ActionCmd); err != nil {
		n.log.Warn("notifier: autonomous action failed", "cmd", sg.ActionCmd, "err", err)
		_ = n.store.UpdateSuggestionStatus(ctx, id, store.StatusIgnored)
		return
	}
	_ = n.store.UpdateSuggestionStatus(ctx, id, store.StatusAccepted)
	_ = n.store.InsertFeedback(ctx, id, "auto_executed")
}
