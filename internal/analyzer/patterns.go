package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/notifier"
	"github.com/wambozi/sigil/internal/store"
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

// editTestFailLoopMin is the minimum number of edit→fail cycles on a single
// file before an "edit-test-fail loop" suggestion is emitted.
const editTestFailLoopMin = 3

// editTestFailLoopWindow is the rolling window within which edit→fail cycles
// are counted.  Cycles older than this are discarded.
const editTestFailLoopWindow = 30 * time.Minute

// stuckEditThreshold is the minimum number of edits to a single file within
// stuckWindow to consider the engineer "stuck" on that file.
const stuckEditThreshold = 5

// stuckWindow is the rolling window for counting edits per file.
const stuckWindow = 15 * time.Minute

// windowSwitchHourlyLimit is the number of Hyprland window focus changes per
// hour above which a window-context-switching suggestion is emitted.
const windowSwitchHourlyLimit = 30

// depChurnThreshold is the minimum number of edits to a single dependency file
// before a dependency-churn suggestion is emitted.
const depChurnThreshold = 4

// Detector runs pure-Go heuristic pattern checks over the local event store
// and returns actionable suggestions.  It never calls the network.
type Detector struct {
	store store.ReadWriter
	log   *slog.Logger
}

// NewDetector creates a Detector backed by the given store.
func NewDetector(s store.ReadWriter, log *slog.Logger) *Detector {
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
		{"edit_test_fail_loop", d.checkEditTestFailLoop},
		{"stuck_detection", d.checkStuckDetection},
		{"dependency_churn", d.checkDependencyChurn},
		{"frequent_files", d.checkFrequentFiles},
		{"build_failure_streak", d.checkBuildFailureStreak},
		{"context_switch_frequency", d.checkContextSwitchFrequency},
		{"window_context_switching", d.checkWindowContextSwitching},
		{"time_of_day", d.checkTimeOfDay},
		{"day_of_week_productivity", d.checkDayOfWeekProductivity},
		{"session_length", d.checkSessionLength},
		{"idle_gaps", d.checkIdleGaps},
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
			if event.IsTestOrBuildCmd(event.CmdFromPayload(te.Payload)) {
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

// checkEditTestFailLoop detects the edit→test-fail→edit-same-file loop pattern
// which is the strongest signal that an engineer is stuck.  For each file, it
// counts cycles of: (1) file edited, (2) test/build fails within editTestWindow,
// (3) same file edited again.  If a file accumulates >= editTestFailLoopMin
// cycles within editTestFailLoopWindow, a high-confidence suggestion is emitted.
func (d *Detector) checkEditTestFailLoop(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	fileEvents, err := d.store.QueryRecentFileEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: edit_test_fail_loop: fetch file events: %w", err)
	}
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: edit_test_fail_loop: fetch terminal events: %w", err)
	}
	if len(fileEvents) == 0 || len(termEvents) == 0 {
		return nil, nil
	}

	// For each file, collect timestamps of edit→fail cycles.
	// A cycle is: file edited at T1, then a test/build fails between T1 and
	// T1+editTestWindow, then the same file is edited again at T2 > fail time.
	// We record T2 as the cycle-completion timestamp.
	type fileState struct {
		editedAt   time.Time // most recent edit timestamp
		awaitFail  bool      // true = we've seen an edit, waiting for a failure
		awaitRedit bool      // true = we've seen a failure, waiting for re-edit
		cycles     []time.Time
	}
	files := make(map[string]*fileState)

	// Build a merged timeline of file and terminal events.
	type timelineEntry struct {
		ts       time.Time
		isFile   bool
		path     string // only for file events
		cmd      string // only for terminal events
		exitCode int    // only for terminal events
	}
	timeline := make([]timelineEntry, 0, len(fileEvents)+len(termEvents))
	for _, fe := range fileEvents {
		path, _ := fe.Payload["path"].(string)
		if path == "" {
			continue
		}
		timeline = append(timeline, timelineEntry{
			ts:     fe.Timestamp,
			isFile: true,
			path:   path,
		})
	}
	for _, te := range termEvents {
		cmd := event.CmdFromPayload(te.Payload)
		if !event.IsTestOrBuildCmd(cmd) {
			continue
		}
		timeline = append(timeline, timelineEntry{
			ts:       te.Timestamp,
			isFile:   false,
			cmd:      cmd,
			exitCode: exitCodeOrZero(te.Payload),
		})
	}

	// Sort by timestamp (both sources are already sorted, but merge order isn't guaranteed).
	for i := 1; i < len(timeline); i++ {
		for j := i; j > 0 && timeline[j].ts.Before(timeline[j-1].ts); j-- {
			timeline[j], timeline[j-1] = timeline[j-1], timeline[j]
		}
	}

	for _, entry := range timeline {
		if entry.isFile {
			fs, ok := files[entry.path]
			if !ok {
				fs = &fileState{}
				files[entry.path] = fs
			}

			if fs.awaitRedit {
				// Completing a cycle: edit → fail → re-edit.
				fs.cycles = append(fs.cycles, entry.ts)
				fs.awaitRedit = false
			}
			// Start tracking a new potential cycle.
			fs.editedAt = entry.ts
			fs.awaitFail = true
		} else {
			// Terminal event: a failing test/build command.
			if entry.exitCode == 0 {
				continue
			}
			// Check all files that have a pending edit within the window.
			for _, fs := range files {
				if !fs.awaitFail {
					continue
				}
				if entry.ts.Sub(fs.editedAt) > editTestWindow {
					fs.awaitFail = false
					continue
				}
				// This failure is correlated with the file edit.
				fs.awaitFail = false
				fs.awaitRedit = true
			}
		}
	}

	var out []notifier.Suggestion
	for path, fs := range files {
		if len(fs.cycles) < editTestFailLoopMin {
			continue
		}

		// Count only cycles within the rolling window.
		cutoff := fs.cycles[len(fs.cycles)-1].Add(-editTestFailLoopWindow)
		count := 0
		for _, t := range fs.cycles {
			if !t.Before(cutoff) {
				count++
			}
		}
		if count < editTestFailLoopMin {
			continue
		}

		out = append(out, notifier.Suggestion{
			Category:   "pattern",
			Confidence: notifier.ConfidenceStrong,
			Title:      "Edit-test-fail loop detected",
			Body: fmt.Sprintf(
				"%s edited and tested %d times, all failing — consider reviewing the error output or rethinking your approach.",
				filepath.Base(path), count,
			),
		})
	}
	return out, nil
}

// checkStuckDetection detects when an engineer is thrashing on a single file:
// editing it many times in a short window while tests are also failing.  This
// complements checkEditTestFailLoop by catching stuck-ness even when the
// edit→test→fail sequence isn't perfectly interleaved — the raw edit velocity
// on one file combined with failures is a strong signal.
func (d *Detector) checkStuckDetection(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	fileEvents, err := d.store.QueryRecentFileEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: stuck_detection: fetch file events: %w", err)
	}
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: stuck_detection: fetch terminal events: %w", err)
	}
	if len(fileEvents) == 0 {
		return nil, nil
	}

	// Collect edit timestamps per file path.
	editsPerFile := make(map[string][]time.Time)
	for _, fe := range fileEvents {
		path, _ := fe.Payload["path"].(string)
		if path == "" {
			continue
		}
		editsPerFile[path] = append(editsPerFile[path], fe.Timestamp)
	}

	// Collect timestamps of test/build failures.
	var failTimes []time.Time
	for _, te := range termEvents {
		if event.IsTestOrBuildCmd(event.CmdFromPayload(te.Payload)) && exitCodeOrZero(te.Payload) != 0 {
			failTimes = append(failTimes, te.Timestamp)
		}
	}

	// For each file, check if any stuckWindow-sized sliding window contains
	// >= stuckEditThreshold edits AND at least one test failure overlaps.
	var out []notifier.Suggestion
	for path, edits := range editsPerFile {
		if len(edits) < stuckEditThreshold {
			continue
		}

		// Sliding window: for each edit, count how many other edits fall
		// within [edit.time, edit.time + stuckWindow].  Edits are already
		// sorted ascending (from the store query).
		for i := 0; i <= len(edits)-stuckEditThreshold; i++ {
			windowStart := edits[i]
			windowEnd := windowStart.Add(stuckWindow)

			// Count edits in this window.
			count := 0
			for j := i; j < len(edits) && !edits[j].After(windowEnd); j++ {
				count++
			}
			if count < stuckEditThreshold {
				continue
			}

			// Check for at least one test failure in the same window.
			hasFailure := false
			for _, ft := range failTimes {
				if !ft.Before(windowStart) && !ft.After(windowEnd) {
					hasFailure = true
					break
				}
			}
			if !hasFailure {
				continue
			}

			out = append(out, notifier.Suggestion{
				Category:   "pattern",
				Confidence: notifier.ConfidenceStrong,
				Title:      "Possible stuck on file",
				Body: fmt.Sprintf(
					"%s edited %d times in %d minutes with test failures — consider stepping back or rubber-ducking the problem.",
					filepath.Base(path), count, int(stuckWindow.Minutes()),
				),
			})
			break // one suggestion per file is enough
		}
	}
	return out, nil
}

// depFileNames contains basenames of dependency/lock files that signal library
// exploration when they change frequently.
var depFileNames = map[string]bool{
	"go.sum":            true,
	"go.mod":            true,
	"package-lock.json": true,
	"yarn.lock":         true,
	"pnpm-lock.yaml":    true,
	"Cargo.lock":        true,
	"Gemfile.lock":      true,
	"poetry.lock":       true,
	"requirements.txt":  true,
	"Pipfile.lock":      true,
	"composer.lock":     true,
	"flake.lock":        true,
}

// checkDependencyChurn detects when dependency/lock files are being modified
// frequently, which signals the engineer is exploring or evaluating libraries.
func (d *Detector) checkDependencyChurn(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	fileEvents, err := d.store.QueryRecentFileEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: dependency_churn: fetch file events: %w", err)
	}
	if len(fileEvents) == 0 {
		return nil, nil
	}

	counts := make(map[string]int)
	for _, fe := range fileEvents {
		path, _ := fe.Payload["path"].(string)
		if path == "" {
			continue
		}
		base := filepath.Base(path)
		if depFileNames[base] {
			counts[path]++
		}
	}

	var out []notifier.Suggestion
	for path, n := range counts {
		if n < depChurnThreshold {
			continue
		}
		out = append(out, notifier.Suggestion{
			Category:   "pattern",
			Confidence: notifier.ConfidenceWeak,
			Title:      "Dependency churn detected",
			Body: fmt.Sprintf(
				"%s changed %d times — exploring new dependencies? Consider documenting your evaluation criteria.",
				filepath.Base(path), n,
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
// command failures and suggests reviewing the error output. When session IDs
// are present, streaks are counted per session and the worst is reported.
func (d *Detector) checkBuildFailureStreak(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: build_failure_streak: %w", err)
	}

	groups := groupBySession(termEvents)
	maxStreak := 0
	for _, events := range groups {
		streak := 0
		for _, te := range events {
			cmd := event.CmdFromPayload(te.Payload)
			if !event.IsTestOrBuildCmd(cmd) {
				continue
			}
			exitCode := exitCodeOrZero(te.Payload)
			if exitCode != 0 {
				streak++
				if streak > maxStreak {
					maxStreak = streak
				}
			} else {
				streak = 0
			}
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
// emits a suggestion when the rate exceeds contextSwitchHourlyLimit. When
// session IDs are present, switches are counted per session and the worst
// hourly rate is reported.
func (d *Detector) checkContextSwitchFrequency(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: context_switch_frequency: %w", err)
	}
	if len(termEvents) == 0 {
		return nil, nil
	}

	type hourKey int64
	hourOf := func(t time.Time) hourKey {
		return hourKey(t.Unix() / 3600)
	}

	groups := groupBySession(termEvents)
	maxSwitches := 0

	for _, events := range groups {
		switchesPerHour := make(map[hourKey]int)
		prevCwd := ""

		for i, te := range events {
			cwd := cwdFromPayload(te.Payload)
			h := hourOf(te.Timestamp)

			if i == 0 {
				prevCwd = cwd
				continue
			}
			if cwd != prevCwd && cwd != "" {
				switchesPerHour[h]++
			}
			prevCwd = cwd
		}

		for _, n := range switchesPerHour {
			if n > maxSwitches {
				maxSwitches = n
			}
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
// required to split them into separate sessions.  30 minutes captures real
// work session boundaries better than the previous 2-hour value.
const sessionGap = 30 * time.Minute

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
// and emits a suggestion when the average exceeds 60 minutes with at least 3
// sessions. When session IDs are present, each session's duration is computed
// from its first to last event; otherwise gap-splitting is used as fallback.
func (d *Detector) checkSessionLength(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: session_length: %w", err)
	}
	if len(termEvents) < 2 {
		return nil, nil
	}

	groups := groupBySession(termEvents)
	var sessions []time.Duration
	for _, events := range groups {
		if len(events) < 2 {
			continue
		}
		sessions = append(sessions, events[len(events)-1].Timestamp.Sub(events[0].Timestamp))
	}

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

// checkIdleGaps reports session count and average duration as a daily insight.
// Uses session ID grouping when available, falling back to gap-splitting.
func (d *Detector) checkIdleGaps(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	termEvents, err := d.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: idle_gaps: %w", err)
	}
	if len(termEvents) < 2 {
		return nil, nil
	}

	groups := groupBySession(termEvents)
	var sessions []time.Duration
	for _, events := range groups {
		if len(events) < 2 {
			continue
		}
		sessions = append(sessions, events[len(events)-1].Timestamp.Sub(events[0].Timestamp))
	}

	// Need at least 2 sessions to report meaningful boundaries.
	if len(sessions) < 2 {
		return nil, nil
	}

	var totalMinutes int
	for _, s := range sessions {
		totalMinutes += int(s.Minutes())
	}
	avgMinutes := totalMinutes / len(sessions)

	return []notifier.Suggestion{{
		Category:   "insight",
		Confidence: notifier.ConfidenceWeak,
		Title:      "Work session summary",
		Body: fmt.Sprintf(
			"You've had %d coding sessions averaging %d minutes each.",
			len(sessions), avgMinutes,
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
		if event.IsTestOrBuildCmd(event.CmdFromPayload(te.Payload)) && exitCodeOrZero(te.Payload) != 0 {
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
			if event.IsTestOrBuildCmd(event.CmdFromPayload(te.Payload)) {
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

// --- Session grouping ------------------------------------------------------

// groupBySession splits terminal events into per-session slices. When
// session_id is present in the payload, events are grouped by that field.
// Events without session_id are grouped using timestamp-gap clustering
// (the existing 30-minute sessionGap heuristic). Returns a map of
// session key → events (sorted by timestamp within each group).
func groupBySession(events []event.Event) map[string][]event.Event {
	groups := make(map[string][]event.Event)
	var noSession []event.Event

	for _, e := range events {
		sid := event.SessionIDFromPayload(e.Payload)
		if sid != "" {
			groups[sid] = append(groups[sid], e)
		} else {
			noSession = append(noSession, e)
		}
	}

	// Fallback: group events without session_id by timestamp gaps.
	if len(noSession) > 0 {
		gapIdx := 0
		key := fmt.Sprintf("_ts_%d", gapIdx)
		groups[key] = append(groups[key], noSession[0])
		for i := 1; i < len(noSession); i++ {
			if noSession[i].Timestamp.Sub(noSession[i-1].Timestamp) > sessionGap {
				gapIdx++
				key = fmt.Sprintf("_ts_%d", gapIdx)
			}
			groups[key] = append(groups[key], noSession[i])
		}
	}

	return groups
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

func cwdFromPayload(payload map[string]any) string {
	cwd, _ := payload["cwd"].(string)
	return cwd
}

// exitCodeOrZero returns the exit code from a terminal event payload,
// defaulting to 0 (success) when the field is absent.
func exitCodeOrZero(payload map[string]any) int {
	code, _ := event.ExitCodeFromPayload(payload)
	return code
}

// checkWindowContextSwitching uses Hyprland window focus events to detect
// excessive window switching.  It buckets focus changes into one-hour slots
// and fires when any hour exceeds windowSwitchHourlyLimit.  Also reports the
// top 3 apps by focus count to help the user see where attention went.
func (d *Detector) checkWindowContextSwitching(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	hyprEvents, err := d.store.QueryHyprlandEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: window_context_switching: %w", err)
	}
	if len(hyprEvents) < 2 {
		return nil, nil
	}

	// Bucket focus events into one-hour slots.
	type hourKey int64
	hourOf := func(t time.Time) hourKey {
		return hourKey(t.Unix() / 3600)
	}

	switchesPerHour := make(map[hourKey]int)
	for _, e := range hyprEvents {
		h := hourOf(e.Timestamp)
		switchesPerHour[h]++
	}

	maxSwitches := 0
	for _, n := range switchesPerHour {
		if n > maxSwitches {
			maxSwitches = n
		}
	}

	if maxSwitches <= windowSwitchHourlyLimit {
		return nil, nil
	}

	// Build a top-apps summary.
	appCounts := make(map[string]int)
	for _, e := range hyprEvents {
		cls, _ := e.Payload["window_class"].(string)
		if cls != "" {
			appCounts[cls]++
		}
	}

	type appEntry struct {
		name  string
		count int
	}
	apps := make([]appEntry, 0, len(appCounts))
	for name, count := range appCounts {
		apps = append(apps, appEntry{name, count})
	}
	for i := 1; i < len(apps); i++ {
		for j := i; j > 0 && apps[j].count > apps[j-1].count; j-- {
			apps[j], apps[j-1] = apps[j-1], apps[j]
		}
	}
	if len(apps) > 3 {
		apps = apps[:3]
	}

	topStr := ""
	for i, a := range apps {
		if i > 0 {
			topStr += ", "
		}
		topStr += fmt.Sprintf("%s (%d)", a.name, a.count)
	}

	return []notifier.Suggestion{{
		Category:   "pattern",
		Confidence: notifier.ConfidenceWeak,
		Title:      "High window switching",
		Body: fmt.Sprintf(
			"Frequent window switching detected — %d focus changes in a single hour. Top apps: %s.",
			maxSwitches, topStr,
		),
	}}, nil
}
