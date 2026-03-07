package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/wambozi/aether/internal/event"
	"github.com/wambozi/aether/internal/notifier"
	"github.com/wambozi/aether/internal/store"
)

// editTestWindow is the maximum elapsed time between a file edit and a
// subsequent test/build command for the pair to be counted toward the
// EditThenTest pattern.
const editTestWindow = 5 * time.Minute

// editTestThreshold is the minimum ratio of (edit→test pairs) / (total edits
// in a directory) required before a suggestion is emitted.
const editTestThreshold = 0.60

// buildFailStreakMin is the number of consecutive build/test failures required
// before a suggestion is emitted.
const buildFailStreakMin = 3

// contextSwitchHourlyLimit is the number of working-directory changes per hour
// above which a context-switching suggestion is emitted.
const contextSwitchHourlyLimit = 6

// Detector runs pure-Go heuristic pattern checks over the local event store
// and returns actionable suggestions.  It never calls the network.
type Detector struct {
	store *store.Store
	log   *slog.Logger
}

// NewDetector creates a Detector backed by the given store.
func NewDetector(s *store.Store, log *slog.Logger) *Detector {
	return &Detector{store: s, log: log}
}

// Detect runs all five pattern checks over the given time window and returns
// any suggestions that meet their confidence thresholds.  A partial failure in
// one check is logged and skipped; the remaining checks still run.
func (d *Detector) Detect(ctx context.Context, window time.Duration) ([]notifier.Suggestion, error) {
	since := time.Now().Add(-window)

	type checkFn func(context.Context, time.Time) ([]notifier.Suggestion, error)
	checks := []struct {
		name string
		fn   checkFn
	}{
		{"edit_then_test", d.checkEditThenTest},
		{"frequent_files", d.checkFrequentFiles},
		{"build_failure_streak", d.checkBuildFailureStreak},
		{"context_switch_frequency", d.checkContextSwitchFrequency},
		{"time_of_day", d.checkTimeOfDay},
		{"day_of_week_productivity", d.checkDayOfWeekProductivity},
		{"session_length", d.checkSessionLength},
		{"ai_query_category_trends", d.checkAIQueryCategoryTrends},
		{"suggestion_acceptance_trend", d.checkSuggestionAcceptanceTrend},
		{"progressive_disclosure", d.checkProgressiveDisclosure},
	}

	var out []notifier.Suggestion
	for _, c := range checks {
		suggestions, err := c.fn(ctx, since)
		if err != nil {
			// Non-fatal: log and continue so one broken check doesn't silence
			// the rest.
			d.log.Warn("patterns: check failed", "check", c.name, "err", err)
			continue
		}
		out = append(out, suggestions...)
	}
	return out, nil
}

// checkEditThenTest detects directories where the user frequently edits a file
// and then runs a test or build command within editTestWindow.  A suggestion is
// emitted for each directory where that ratio exceeds editTestThreshold.
func (d *Detector) checkEditThenTest(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	fileEvents, err := d.store.QueryRecentFileEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: edit_then_test: fetch file events: %w", err)
	}
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: edit_then_test: fetch terminal events: %w", err)
	}
	if len(fileEvents) == 0 || len(termEvents) == 0 {
		return nil, nil
	}

	// editCount and followedCount are keyed by directory.
	editCount := make(map[string]int)
	followedCount := make(map[string]int)

	for _, fe := range fileEvents {
		dir := dirFromPayload(fe.Payload)
		if dir == "" {
			continue
		}
		editCount[dir]++

		// Scan forward through terminal events that fall within the window
		// after this file edit.
		deadline := fe.Timestamp.Add(editTestWindow)
		for _, te := range termEvents {
			if te.Timestamp.Before(fe.Timestamp) {
				continue
			}
			if te.Timestamp.After(deadline) {
				break
			}
			if isTestOrBuildCmd(cmdFromPayload(te.Payload)) {
				followedCount[dir]++
				break // count at most one test run per edit event
			}
		}
	}

	var out []notifier.Suggestion
	for dir, total := range editCount {
		if total == 0 {
			continue
		}
		ratio := float64(followedCount[dir]) / float64(total)
		if ratio < editTestThreshold {
			continue
		}
		out = append(out, notifier.Suggestion{
			Category:   "pattern",
			Confidence: ratio,
			Title:      "Edit-then-test pattern detected",
			Body: fmt.Sprintf(
				"You run tests after %.0f%% of edits in %s — consider a file-watch test runner.",
				ratio*100, dir,
			),
		})
	}
	return out, nil
}

// checkFrequentFiles surfaces files that appear in today's top-5 most-edited
// list but were absent from yesterday's top-5 — indicating an unusual focus
// shift.
//
// "Today" is the last 24 hours; "yesterday" is the 24-hour window before that.
// Both sets are derived from a single query (last 48h) partitioned in Go so
// the store only needs a single-sided time bound.
func (d *Detector) checkFrequentFiles(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	now := time.Now()
	todayStart := now.Add(-24 * time.Hour)
	yesterdayStart := todayStart.Add(-24 * time.Hour)

	// Fetch all file events for the last 48 hours and partition them into
	// today and yesterday buckets in Go — avoids needing an upper-bound on
	// the store query.
	allEvents, err := d.store.QueryRecentFileEvents(ctx, yesterdayStart)
	if err != nil {
		return nil, fmt.Errorf("patterns: frequent_files: fetch events: %w", err)
	}
	if len(allEvents) == 0 {
		return nil, nil
	}

	todayCounts := make(map[string]int64)
	yesterdayCounts := make(map[string]int64)
	for _, e := range allEvents {
		path, _ := e.Payload["path"].(string)
		if path == "" {
			continue
		}
		if !e.Timestamp.Before(todayStart) {
			todayCounts[path]++
		} else {
			yesterdayCounts[path]++
		}
	}

	todayTop := topN(todayCounts, 5)
	yesterdayTop := topN(yesterdayCounts, 5)

	if len(todayTop) == 0 {
		return nil, nil
	}

	yesterdaySet := make(map[string]struct{}, len(yesterdayTop))
	for _, f := range yesterdayTop {
		yesterdaySet[f.Path] = struct{}{}
	}

	var out []notifier.Suggestion
	for _, f := range todayTop {
		if _, seen := yesterdaySet[f.Path]; seen {
			continue
		}
		out = append(out, notifier.Suggestion{
			Category:   "pattern",
			Confidence: notifier.ConfidenceWeak,
			Title:      "Unusual file focus",
			Body: fmt.Sprintf(
				"You're spending more time in %s than usual (%d edits today, not in yesterday's top 5).",
				filepath.Base(f.Path), f.Count,
			),
		})
	}
	return out, nil
}

// topN returns the n file paths with the highest counts from the given map,
// sorted by count descending.
func topN(counts map[string]int64, n int) []store.FileEditCount {
	out := make([]store.FileEditCount, 0, len(counts))
	for path, count := range counts {
		out = append(out, store.FileEditCount{Path: path, Count: count})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Count > out[j-1].Count; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// checkBuildFailureStreak detects three or more consecutive build or test
// command failures and suggests reviewing the error output.
func (d *Detector) checkBuildFailureStreak(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: build_failure_streak: %w", err)
	}

	streak := 0
	maxStreak := 0
	for _, te := range termEvents {
		cmd := cmdFromPayload(te.Payload)
		if !isTestOrBuildCmd(cmd) {
			continue
		}
		exitCode := exitCodeFromPayload(te.Payload)
		if exitCode != 0 {
			streak++
			if streak > maxStreak {
				maxStreak = streak
			}
		} else {
			streak = 0
		}
	}

	if maxStreak < buildFailStreakMin {
		return nil, nil
	}
	return []notifier.Suggestion{{
		Category:   "pattern",
		Confidence: notifier.ConfidenceModerate,
		Title:      fmt.Sprintf("%d consecutive build/test failures", maxStreak),
		Body:       "You've had multiple failures in a row — want a summary of the errors?",
	}}, nil
}

// checkContextSwitchFrequency counts working-directory changes per hour and
// emits a suggestion when the rate exceeds contextSwitchHourlyLimit.
func (d *Detector) checkContextSwitchFrequency(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: context_switch_frequency: %w", err)
	}
	if len(termEvents) == 0 {
		return nil, nil
	}

	// Bucket events into one-hour slots keyed by the hour boundary (Unix
	// seconds truncated to the hour).
	type hourKey int64
	hourOf := func(t time.Time) hourKey {
		return hourKey(t.Unix() / 3600)
	}

	// switchesPerHour counts directory transitions within each hour bucket.
	switchesPerHour := make(map[hourKey]int)
	prevCwd := ""
	prevHour := hourKey(0)

	for i, te := range termEvents {
		cwd := cwdFromPayload(te.Payload)
		h := hourOf(te.Timestamp)

		if i == 0 {
			prevCwd = cwd
			prevHour = h
			continue
		}
		if cwd != prevCwd && cwd != "" {
			switchesPerHour[prevHour]++
		}
		prevCwd = cwd
		prevHour = h
	}

	maxSwitches := 0
	for _, n := range switchesPerHour {
		if n > maxSwitches {
			maxSwitches = n
		}
	}

	if maxSwitches <= contextSwitchHourlyLimit {
		return nil, nil
	}
	return []notifier.Suggestion{{
		Category:   "pattern",
		Confidence: notifier.ConfidenceWeak,
		Title:      "High context-switching",
		Body: fmt.Sprintf(
			"High context-switching today — %d directory changes in a single hour.",
			maxSwitches,
		),
	}}, nil
}

// checkTimeOfDay identifies the hour of day with the most file edits over the
// window and surfaces it as a productive-hours insight for the daily digest.
func (d *Detector) checkTimeOfDay(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	fileEvents, err := d.store.QueryRecentFileEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: time_of_day: %w", err)
	}
	if len(fileEvents) == 0 {
		return nil, nil
	}

	// editsByHour counts file edits per hour-of-day (0–23).
	editsByHour := make(map[int]int, 24)
	for _, fe := range fileEvents {
		editsByHour[fe.Timestamp.Hour()]++
	}

	peakHour := 0
	peakCount := 0
	for h, n := range editsByHour {
		if n > peakCount {
			peakCount = n
			peakHour = h
		}
	}

	// Only surface if there is a meaningful concentration of activity.
	if peakCount < 5 {
		return nil, nil
	}
	return []notifier.Suggestion{{
		Category:   "insight",
		Confidence: notifier.ConfidenceWeak,
		Title:      "Productive hour identified",
		Body: fmt.Sprintf(
			"Your most active coding hour is %02d:00–%02d:00 (%d file edits).",
			peakHour, peakHour+1, peakCount,
		),
	}}, nil
}

// sessionGap is the minimum idle time between consecutive terminal events
// required to split them into separate sessions.
const sessionGap = 2 * time.Hour

// checkDayOfWeekProductivity groups file-edit counts by weekday, finds the most
// productive day, and emits a suggestion when peak is >= 2x trough and peak has >= 10 edits.
func (d *Detector) checkDayOfWeekProductivity(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	fileEvents, err := d.store.QueryRecentFileEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: day_of_week_productivity: %w", err)
	}
	if len(fileEvents) == 0 {
		return nil, nil
	}

	editsByDay := make(map[time.Weekday]int)
	for _, fe := range fileEvents {
		editsByDay[fe.Timestamp.Weekday()]++
	}

	if len(editsByDay) < 2 {
		return nil, nil
	}

	peakDay := time.Sunday
	peakCount := 0
	troughCount := int(^uint(0) >> 1) // max int
	for day, n := range editsByDay {
		if n > peakCount {
			peakCount = n
			peakDay = day
		}
		if n < troughCount {
			troughCount = n
		}
	}

	if peakCount < 10 || troughCount == 0 || peakCount < 2*troughCount {
		return nil, nil
	}

	return []notifier.Suggestion{{
		Category:   "insight",
		Confidence: notifier.ConfidenceWeak,
		Title:      "Day-of-week productivity pattern",
		Body: fmt.Sprintf(
			"Your most productive day is %s with %d edits — %dx more than your quietest day.",
			peakDay, peakCount, peakCount/troughCount,
		),
	}}, nil
}

// checkSessionLength computes average coding session length from terminal events
// and emits a suggestion when the average exceeds 60 minutes with at least 3 sessions.
func (d *Detector) checkSessionLength(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: session_length: %w", err)
	}
	if len(termEvents) < 2 {
		return nil, nil
	}

	var sessions []time.Duration
	sessionStart := termEvents[0].Timestamp
	lastTS := termEvents[0].Timestamp

	for i := 1; i < len(termEvents); i++ {
		gap := termEvents[i].Timestamp.Sub(lastTS)
		if gap > sessionGap {
			sessions = append(sessions, lastTS.Sub(sessionStart))
			sessionStart = termEvents[i].Timestamp
		}
		lastTS = termEvents[i].Timestamp
	}
	// Close the final session.
	sessions = append(sessions, lastTS.Sub(sessionStart))

	if len(sessions) < 3 {
		return nil, nil
	}

	var totalMinutes int
	for _, s := range sessions {
		totalMinutes += int(s.Minutes())
	}
	avgMinutes := totalMinutes / len(sessions)

	if avgMinutes <= 60 {
		return nil, nil
	}

	return []notifier.Suggestion{{
		Category:   "insight",
		Confidence: notifier.ConfidenceWeak,
		Title:      "Long coding sessions",
		Body: fmt.Sprintf(
			"Average coding session: %d minutes (based on terminal activity).",
			avgMinutes,
		),
	}}, nil
}

// checkAIQueryCategoryTrends tallies AI interactions by QueryCategory and emits
// a suggestion for the top category if it accounts for >= 50%% of queries and
// there are >= 5 total.
func (d *Detector) checkAIQueryCategoryTrends(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	interactions, err := d.store.QueryAIInteractions(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: ai_query_category_trends: %w", err)
	}
	if len(interactions) < 5 {
		return nil, nil
	}

	counts := make(map[string]int)
	for _, ai := range interactions {
		if ai.QueryCategory != "" {
			counts[ai.QueryCategory]++
		}
	}

	topCat := ""
	topCount := 0
	for cat, n := range counts {
		if n > topCount {
			topCount = n
			topCat = cat
		}
	}

	total := len(interactions)
	pct := topCount * 100 / total
	if pct < 50 {
		return nil, nil
	}

	return []notifier.Suggestion{{
		Category:   "insight",
		Confidence: notifier.ConfidenceModerate,
		Title:      "AI query category trend",
		Body: fmt.Sprintf(
			"Most of your AI queries are about %s (%d%% of recent queries).",
			topCat, pct,
		),
	}}, nil
}

// checkSuggestionAcceptanceTrend checks the suggestion acceptance rate and emits
// a positive reinforcement or adjustment suggestion.
func (d *Detector) checkSuggestionAcceptanceTrend(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	rate, err := d.store.QuerySuggestionAcceptanceRate(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: suggestion_acceptance_trend: %w", err)
	}

	if rate >= 0.7 {
		return []notifier.Suggestion{{
			Category:   "insight",
			Confidence: notifier.ConfidenceWeak,
			Title:      "High suggestion acceptance",
			Body: fmt.Sprintf(
				"You're accepting %.0f%% of suggestions — the system is well-tuned to your workflow.",
				rate*100,
			),
		}}, nil
	}

	if rate < 0.3 {
		resolved, err := d.store.QueryResolvedSuggestionCount(ctx, since)
		if err != nil {
			return nil, fmt.Errorf("patterns: suggestion_acceptance_trend: count: %w", err)
		}
		if resolved >= 10 {
			return []notifier.Suggestion{{
				Category:   "insight",
				Confidence: notifier.ConfidenceWeak,
				Title:      "Low suggestion acceptance",
				Body: fmt.Sprintf(
					"Only %.0f%% of suggestions accepted — consider adjusting your notification level.",
					rate*100,
				),
			}}, nil
		}
	}

	return nil, nil
}

// --- Progressive AI disclosure ---------------------------------------------

// AITier classifies the engineer's AI adoption level.
type AITier int

const (
	TierObserver   AITier = 0 // no AI queries
	TierExplorer   AITier = 1 // < 5 queries in last 7 days
	TierIntegrator AITier = 2 // 5–20 queries in last 7 days
	TierNative     AITier = 3 // 20+ queries in last 7 days
)

// detectAITier returns the tier based on the number of AI interactions.
func detectAITier(interactions []event.AIInteraction) AITier {
	n := len(interactions)
	switch {
	case n == 0:
		return TierObserver
	case n < 5:
		return TierExplorer
	case n <= 20:
		return TierIntegrator
	default:
		return TierNative
	}
}

// checkProgressiveDisclosure computes the user's AI tier and emits contextual
// suggestions that nudge users toward deeper AI adoption. The current tier is
// persisted in the patterns table so it survives restarts.
func (d *Detector) checkProgressiveDisclosure(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	// Use a 7-day window for tier detection regardless of the detector window.
	tierSince := time.Now().Add(-7 * 24 * time.Hour)
	interactions, err := d.store.QueryAIInteractions(ctx, tierSince)
	if err != nil {
		return nil, fmt.Errorf("patterns: progressive_disclosure: query interactions: %w", err)
	}

	tier := detectAITier(interactions)

	// Persist the tier.
	_ = d.store.InsertPattern(ctx, "ai_tier", map[string]any{"tier": int(tier)})

	switch tier {
	case TierObserver:
		// Tier 0→1: nudge if build failures detected.
		return d.progressiveTier0(ctx, since)
	case TierExplorer:
		// Tier 1→2: nudge if edit-then-test ratio is high.
		return d.progressiveTier1(ctx, since)
	case TierIntegrator:
		// Tier 2→3: codebase-aware prompts.
		return d.progressiveTier2(ctx, since)
	default:
		// TierNative: no disclosure needed.
		return nil, nil
	}
}

// progressiveTier0 nudges observers toward their first AI query when build failures are detected.
func (d *Detector) progressiveTier0(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, err
	}
	failures := 0
	for _, te := range termEvents {
		if isTestOrBuildCmd(cmdFromPayload(te.Payload)) && exitCodeFromPayload(te.Payload) != 0 {
			failures++
		}
	}
	if failures < buildFailStreakMin {
		return nil, nil
	}
	return []notifier.Suggestion{{
		Category:   "ai_discovery",
		Confidence: notifier.ConfidenceModerate,
		Title:      "Try the AI assistant",
		Body:       "Stuck on build failures? Try Alt+Tab and ask: 'why is my build failing?'",
	}}, nil
}

// progressiveTier1 nudges explorers toward deeper integration when edit-then-test patterns are strong.
func (d *Detector) progressiveTier1(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	fileEvents, err := d.store.QueryRecentFileEvents(ctx, since)
	if err != nil {
		return nil, err
	}
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, err
	}
	if len(fileEvents) == 0 || len(termEvents) == 0 {
		return nil, nil
	}

	// Find the directory with the highest edit-then-test ratio.
	editCount := make(map[string]int)
	followedCount := make(map[string]int)
	for _, fe := range fileEvents {
		dir := dirFromPayload(fe.Payload)
		if dir == "" {
			continue
		}
		editCount[dir]++
		deadline := fe.Timestamp.Add(editTestWindow)
		for _, te := range termEvents {
			if te.Timestamp.Before(fe.Timestamp) {
				continue
			}
			if te.Timestamp.After(deadline) {
				break
			}
			if isTestOrBuildCmd(cmdFromPayload(te.Payload)) {
				followedCount[dir]++
				break
			}
		}
	}

	bestDir := ""
	bestRatio := 0.0
	for dir, total := range editCount {
		if total == 0 {
			continue
		}
		ratio := float64(followedCount[dir]) / float64(total)
		if ratio > bestRatio {
			bestRatio = ratio
			bestDir = dir
		}
	}

	if bestRatio < editTestThreshold {
		return nil, nil
	}
	return []notifier.Suggestion{{
		Category:   "ai_discovery",
		Confidence: notifier.ConfidenceModerate,
		Title:      "Automate your test workflow",
		Body: fmt.Sprintf(
			"You always run tests after edits in %s. Alt+Tab and ask: 'set up a file-watch test runner for this project.'",
			bestDir,
		),
	}}, nil
}

// progressiveTier2 nudges integrators toward native usage with codebase-aware prompts.
func (d *Detector) progressiveTier2(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	fileEvents, err := d.store.QueryRecentFileEvents(ctx, since)
	if err != nil {
		return nil, err
	}
	if len(fileEvents) == 0 {
		return nil, nil
	}

	counts := make(map[string]int64)
	for _, e := range fileEvents {
		path, _ := e.Payload["path"].(string)
		if path != "" {
			counts[path]++
		}
	}
	top := topN(counts, 1)
	if len(top) == 0 {
		return nil, nil
	}

	return []notifier.Suggestion{{
		Category:   "ai_discovery",
		Confidence: notifier.ConfidenceStrong,
		Title:      "Deep-dive with AI",
		Body: fmt.Sprintf(
			"You're spending time in %s. Alt+Tab and ask: 'summarize this module and suggest improvements.'",
			filepath.Base(top[0].Path),
		),
	}}, nil
}

// --- Payload helpers -------------------------------------------------------
//
// Terminal events carry a JSON payload with keys "cmd", "exit_code", "cwd".
// File events carry a payload with key "path".
// These helpers centralise payload extraction so the pattern checks stay readable.

func dirFromPayload(payload map[string]any) string {
	path, _ := payload["path"].(string)
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}

func cmdFromPayload(payload map[string]any) string {
	cmd, _ := payload["cmd"].(string)
	return cmd
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
	return 0
}

func cwdFromPayload(payload map[string]any) string {
	cwd, _ := payload["cwd"].(string)
	return cwd
}

// isTestOrBuildCmd reports whether cmd looks like a test or build invocation.
// The list is intentionally conservative — false negatives are safer than
// false positives for streak detection.
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
