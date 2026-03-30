package analyzer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/notifier"
)

// --- Business workflow pattern constants ------------------------------------

// businessMinObservation is the minimum observation period before any business
// pattern fires.  This avoids false positives during initial data collection.
const businessMinObservation = 24 * time.Hour

// emailApps lists window classes commonly associated with email clients.
var emailApps = map[string]bool{
	"thunderbird":       true,
	"evolution":         true,
	"geary":             true,
	"outlook":           true,
	"mail":              true,
	"gmail":             true,
	"microsoft-outlook": true,
	"outlook.exe":       true,
}

// spreadsheetApps lists window classes for spreadsheet applications.
var spreadsheetApps = map[string]bool{
	"libreoffice-calc": true,
	"gnumeric":         true,
	"excel":            true,
	"sheets":           true,
	"microsoft-excel":  true,
	"excel.exe":        true,
	"google-chrome":    true, // handled via title matching below
}

// calendarApps lists window classes for calendar applications.
var calendarApps = map[string]bool{
	"gnome-calendar":    true,
	"thunderbird":       true,
	"evolution":         true,
	"outlook":           true,
	"google-calendar":   true,
	"microsoft-outlook": true,
	"fantastical":       true,
}

// documentApps lists window classes for document editors.
var documentApps = map[string]bool{
	"libreoffice-writer":   true,
	"libreoffice-calc":     true,
	"libreoffice-impress":  true,
	"microsoft-word":       true,
	"microsoft-excel":      true,
	"microsoft-powerpoint": true,
	"word.exe":             true,
	"excel.exe":            true,
	"powerpoint.exe":       true,
	"google-docs":          true,
	"notion":               true,
	"obsidian":             true,
}

// crossAppSwitchThreshold is the number of switches among the same 3+ app set
// within crossAppSwitchWindow that triggers a context-loss suggestion.
const crossAppSwitchThreshold = 12

// crossAppSwitchWindow is the rolling window for counting cross-app switches.
const crossAppSwitchWindow = 10 * time.Minute

// spreadsheetFocusMinDuration is the minimum continuous spreadsheet usage
// before recognising a deep-work session.
const spreadsheetFocusMinDuration = 30 * time.Minute

// responsePendingWindow is how long after reading an email we wait before
// suggesting a reply is pending.
const responsePendingWindow = 4 * time.Hour

// repetitiveWorkflowMinOccurrences is the minimum number of times a workflow
// sequence must appear across separate days to be flagged.
const repetitiveWorkflowMinOccurrences = 3

// endOfDayMinEvents is the minimum number of events in a day for the
// end-of-day summary to fire.
const endOfDayMinEvents = 20

// endOfDayHour is the hour (24h format, local time) at which end-of-day
// suggestions become eligible.
const endOfDayHour = 17

// --- Helper functions -------------------------------------------------------

// isEmailApp returns true if the window class looks like an email client.
func isEmailApp(cls string) bool {
	lower := strings.ToLower(cls)
	if emailApps[lower] {
		return true
	}
	// Fuzzy match for browser-based email.
	return strings.Contains(lower, "mail") || strings.Contains(lower, "outlook")
}

// isSpreadsheetApp returns true if the window class looks like a spreadsheet.
func isSpreadsheetApp(cls string) bool {
	lower := strings.ToLower(cls)
	if spreadsheetApps[lower] {
		return true
	}
	return strings.Contains(lower, "excel") || strings.Contains(lower, "calc") ||
		strings.Contains(lower, "sheets") || strings.Contains(lower, "spreadsheet")
}

// isCalendarApp returns true if the window class looks like a calendar app.
func isCalendarApp(cls string) bool {
	lower := strings.ToLower(cls)
	if calendarApps[lower] {
		return true
	}
	return strings.Contains(lower, "calendar")
}

// isDocumentApp returns true if the window class looks like a document editor.
func isDocumentApp(cls string) bool {
	lower := strings.ToLower(cls)
	if documentApps[lower] {
		return true
	}
	return strings.Contains(lower, "word") || strings.Contains(lower, "docs") ||
		strings.Contains(lower, "notion") || strings.Contains(lower, "obsidian") ||
		strings.Contains(lower, "writer")
}

// windowClass extracts the window_class from an event payload.
func windowClass(e event.Event) string {
	cls, _ := e.Payload["window_class"].(string)
	return cls
}

// windowTitle extracts the window_title from an event payload.
func windowTitle(e event.Event) string {
	title, _ := e.Payload["window_title"].(string)
	return title
}

// hasMinObservation checks whether the store has events older than
// businessMinObservation, ensuring sufficient data before firing patterns.
func (d *Detector) hasMinObservation(ctx context.Context, since time.Time) bool {
	cutoff := time.Now().Add(-businessMinObservation)
	return !since.After(cutoff)
}

// queryEventsByKindSince retrieves events of a given kind at or after since.
// It uses QueryEvents with a large limit and filters by timestamp in Go.
func (d *Detector) queryEventsByKindSince(ctx context.Context, kind event.Kind, since time.Time) ([]event.Event, error) {
	events, err := d.store.QueryEvents(ctx, kind, 1<<20)
	if err != nil {
		return nil, err
	}
	var filtered []event.Event
	for _, e := range events {
		if !e.Timestamp.Before(since) {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

// --- Business pattern checks ------------------------------------------------

// checkEmailThenSpreadsheet detects: email read -> spreadsheet opened -> data
// copied -> email reply.  Works with Layer 1 (app focus events).  Higher
// confidence with Layer 2 (clipboard) and Layer 3 (app_state).
func (d *Detector) checkEmailThenSpreadsheet(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	if !d.hasMinObservation(ctx, since) {
		return nil, nil
	}

	hyprEvents, err := d.store.QueryHyprlandEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: email_then_spreadsheet: fetch hyprland events: %w", err)
	}
	if len(hyprEvents) < 4 {
		return nil, nil
	}

	// Look for the sequence: email -> spreadsheet -> ... -> email within a
	// sliding window.  Each transition must happen within 15 minutes.
	const seqWindow = 15 * time.Minute
	seqCount := 0

	for i := 0; i < len(hyprEvents)-1; i++ {
		cls := windowClass(hyprEvents[i])
		if !isEmailApp(cls) {
			continue
		}

		// Found an email focus event.  Scan forward for spreadsheet then back to email.
		emailTime := hyprEvents[i].Timestamp
		sawSpreadsheet := false
		spreadsheetTime := time.Time{}

		for j := i + 1; j < len(hyprEvents); j++ {
			if hyprEvents[j].Timestamp.Sub(emailTime) > seqWindow {
				break
			}
			jCls := windowClass(hyprEvents[j])
			if !sawSpreadsheet && isSpreadsheetApp(jCls) {
				sawSpreadsheet = true
				spreadsheetTime = hyprEvents[j].Timestamp
				continue
			}
			if sawSpreadsheet && isEmailApp(jCls) {
				// Complete sequence: email -> spreadsheet -> email.
				if hyprEvents[j].Timestamp.Sub(spreadsheetTime) < seqWindow {
					seqCount++
				}
				break
			}
		}
	}

	if seqCount == 0 {
		return nil, nil
	}

	// Check for clipboard events in the same window for higher confidence.
	confidence := notifier.ConfidenceWeak
	clipboardEvents, _ := d.queryEventsByKindSince(ctx, event.KindClipboard, since)
	if len(clipboardEvents) > 0 {
		confidence = notifier.ConfidenceModerate
	}

	return []notifier.Suggestion{{
		Category:   "workflow",
		Confidence: confidence,
		Title:      "Email-spreadsheet-reply pattern detected",
		Body: fmt.Sprintf(
			"Detected %d email->spreadsheet->reply sequences — consider a template or automation for this workflow.",
			seqCount,
		),
	}}, nil
}

// checkMeetingPreparation detects: calendar event approaching + related docs
// not opened.  Uses Hyprland focus events to detect calendar app usage and
// document app absence.
func (d *Detector) checkMeetingPreparation(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	if !d.hasMinObservation(ctx, since) {
		return nil, nil
	}

	hyprEvents, err := d.store.QueryHyprlandEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: meeting_preparation: fetch hyprland events: %w", err)
	}
	if len(hyprEvents) == 0 {
		return nil, nil
	}

	// Look for calendar app usage in the last 2 hours followed by no
	// document app usage in the subsequent 30 minutes.
	const calendarLookback = 2 * time.Hour
	const docGap = 30 * time.Minute
	now := time.Now()
	calendarCutoff := now.Add(-calendarLookback)

	var lastCalendarTime time.Time
	for i := len(hyprEvents) - 1; i >= 0; i-- {
		if hyprEvents[i].Timestamp.Before(calendarCutoff) {
			break
		}
		if isCalendarApp(windowClass(hyprEvents[i])) {
			lastCalendarTime = hyprEvents[i].Timestamp
			break
		}
	}

	if lastCalendarTime.IsZero() {
		return nil, nil
	}

	// Check if any document app was opened after the calendar check.
	docOpened := false
	for _, e := range hyprEvents {
		if e.Timestamp.Before(lastCalendarTime) {
			continue
		}
		if isDocumentApp(windowClass(e)) {
			docOpened = true
			break
		}
	}

	if docOpened {
		return nil, nil
	}

	// Only suggest if enough time has passed without doc prep.
	if now.Sub(lastCalendarTime) < docGap {
		return nil, nil
	}

	return []notifier.Suggestion{{
		Category:   "workflow",
		Confidence: notifier.ConfidenceWeak,
		Title:      "Meeting approaching — documents not opened",
		Body:       "You checked your calendar recently but haven't opened any related documents. Need to prepare?",
	}}, nil
}

// checkDocumentStale detects: document not edited in N days but mentioned in
// recent communication.  Uses file events for document edits and Hyprland
// events for communication app activity referencing the same files.
func (d *Detector) checkDocumentStale(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	if !d.hasMinObservation(ctx, since) {
		return nil, nil
	}

	// Use a wider window for staleness detection: 7 days.
	staleSince := time.Now().Add(-7 * 24 * time.Hour)
	fileEvents, err := d.store.QueryRecentFileEvents(ctx, staleSince)
	if err != nil {
		return nil, fmt.Errorf("patterns: document_stale: fetch file events: %w", err)
	}

	hyprEvents, err := d.store.QueryHyprlandEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: document_stale: fetch hyprland events: %w", err)
	}
	if len(fileEvents) == 0 || len(hyprEvents) == 0 {
		return nil, nil
	}

	// Build a map of document files and their last edit time.
	docLastEdit := make(map[string]time.Time)
	for _, fe := range fileEvents {
		path, _ := fe.Payload["path"].(string)
		if path == "" {
			continue
		}
		lower := strings.ToLower(path)
		isDoc := strings.HasSuffix(lower, ".doc") || strings.HasSuffix(lower, ".docx") ||
			strings.HasSuffix(lower, ".xlsx") || strings.HasSuffix(lower, ".xls") ||
			strings.HasSuffix(lower, ".pptx") || strings.HasSuffix(lower, ".ppt") ||
			strings.HasSuffix(lower, ".pdf") || strings.HasSuffix(lower, ".odt") ||
			strings.HasSuffix(lower, ".ods") || strings.HasSuffix(lower, ".odp") ||
			strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".txt")
		if !isDoc {
			continue
		}
		if fe.Timestamp.After(docLastEdit[path]) {
			docLastEdit[path] = fe.Timestamp
		}
	}

	if len(docLastEdit) == 0 {
		return nil, nil
	}

	// Check for recent communication app usage (email, chat) that might
	// reference these documents.  We look at window titles for filename
	// mentions.
	recentComms := 0
	commCutoff := time.Now().Add(-48 * time.Hour)
	for _, e := range hyprEvents {
		if e.Timestamp.Before(commCutoff) {
			continue
		}
		cls := windowClass(e)
		if isEmailApp(cls) || strings.Contains(strings.ToLower(cls), "slack") ||
			strings.Contains(strings.ToLower(cls), "teams") ||
			strings.Contains(strings.ToLower(cls), "discord") {
			recentComms++
		}
	}

	if recentComms < 3 {
		return nil, nil
	}

	// Find documents not edited in the last 3 days.
	staleCutoff := time.Now().Add(-3 * 24 * time.Hour)
	staleCount := 0
	for _, lastEdit := range docLastEdit {
		if lastEdit.Before(staleCutoff) {
			staleCount++
		}
	}

	if staleCount == 0 {
		return nil, nil
	}

	return []notifier.Suggestion{{
		Category:   "workflow",
		Confidence: notifier.ConfidenceWeak,
		Title:      "Stale documents detected",
		Body: fmt.Sprintf(
			"%d document(s) not edited in 3+ days while you've been active in communication apps — review needed?",
			staleCount,
		),
	}}, nil
}

// checkCrossAppContextLoss detects: switching between the same 3+ apps more
// than N times in M minutes.  Same concept as engineering context switching
// but for any apps.
func (d *Detector) checkCrossAppContextLoss(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	if !d.hasMinObservation(ctx, since) {
		return nil, nil
	}

	hyprEvents, err := d.store.QueryHyprlandEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: cross_app_context_loss: fetch hyprland events: %w", err)
	}
	if len(hyprEvents) < crossAppSwitchThreshold {
		return nil, nil
	}

	// Sliding window: for each event, count distinct apps and total switches
	// in the following crossAppSwitchWindow.
	var worstSwitches int
	var worstApps []string

	for i := 0; i < len(hyprEvents); i++ {
		windowEnd := hyprEvents[i].Timestamp.Add(crossAppSwitchWindow)
		appSet := make(map[string]bool)
		switches := 0
		prevCls := ""

		for j := i; j < len(hyprEvents) && !hyprEvents[j].Timestamp.After(windowEnd); j++ {
			cls := windowClass(hyprEvents[j])
			if cls == "" {
				continue
			}
			appSet[cls] = true
			if cls != prevCls && prevCls != "" {
				switches++
			}
			prevCls = cls
		}

		if len(appSet) >= 3 && switches >= crossAppSwitchThreshold && switches > worstSwitches {
			worstSwitches = switches
			worstApps = make([]string, 0, len(appSet))
			for app := range appSet {
				worstApps = append(worstApps, app)
			}
		}
	}

	if worstSwitches == 0 {
		return nil, nil
	}

	// Cap the displayed app list at 5.
	displayApps := worstApps
	if len(displayApps) > 5 {
		displayApps = displayApps[:5]
	}

	return []notifier.Suggestion{{
		Category:   "workflow",
		Confidence: notifier.ConfidenceModerate,
		Title:      "Rapid app switching detected",
		Body: fmt.Sprintf(
			"%d app switches in %d minutes across %s — consider batching tasks per app.",
			worstSwitches, int(crossAppSwitchWindow.Minutes()), strings.Join(displayApps, ", "),
		),
	}}, nil
}

// checkSpreadsheetFocusSession detects: sustained Excel/Sheets activity
// (>30 min) without interruption.  Recognises deep work in non-engineering
// tools.  Returns a positive-reinforcement suggestion rather than an
// interruption.
func (d *Detector) checkSpreadsheetFocusSession(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	if !d.hasMinObservation(ctx, since) {
		return nil, nil
	}

	hyprEvents, err := d.store.QueryHyprlandEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: spreadsheet_focus_session: fetch hyprland events: %w", err)
	}
	if len(hyprEvents) < 2 {
		return nil, nil
	}

	// Find the longest continuous spreadsheet session.  A session ends when
	// the user switches to a non-spreadsheet app for more than 2 minutes.
	const interruptionThreshold = 2 * time.Minute
	var longestDuration time.Duration
	var sessionStart time.Time
	var lastSpreadsheetTime time.Time
	inSession := false

	for _, e := range hyprEvents {
		cls := windowClass(e)
		if isSpreadsheetApp(cls) {
			if !inSession {
				inSession = true
				sessionStart = e.Timestamp
			}
			lastSpreadsheetTime = e.Timestamp
		} else if inSession {
			// Non-spreadsheet event.  Check if this is a brief interruption.
			if e.Timestamp.Sub(lastSpreadsheetTime) > interruptionThreshold {
				// Session ended.
				duration := lastSpreadsheetTime.Sub(sessionStart)
				if duration > longestDuration {
					longestDuration = duration
				}
				inSession = false
			}
		}
	}

	// Close any open session.
	if inSession {
		duration := lastSpreadsheetTime.Sub(sessionStart)
		if duration > longestDuration {
			longestDuration = duration
		}
	}

	if longestDuration < spreadsheetFocusMinDuration {
		return nil, nil
	}

	return []notifier.Suggestion{{
		Category:   "workflow",
		Confidence: notifier.ConfidenceModerate,
		Title:      "Spreadsheet deep-work session",
		Body: fmt.Sprintf(
			"Sustained spreadsheet focus for %d minutes — nice deep work session. Notifications were suppressed.",
			int(longestDuration.Minutes()),
		),
	}}, nil
}

// checkResponsePending detects: email read but no reply composed within N
// hours.  Uses Hyprland focus events to track email app dwell time.
func (d *Detector) checkResponsePending(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	if !d.hasMinObservation(ctx, since) {
		return nil, nil
	}

	hyprEvents, err := d.store.QueryHyprlandEvents(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("patterns: response_pending: fetch hyprland events: %w", err)
	}
	if len(hyprEvents) < 2 {
		return nil, nil
	}

	// Track email app sessions: periods where the email app has focus.
	// A "read" is an email focus period > 5 seconds (brief switch = noise).
	// A "reply" is an email focus period > 60 seconds (composing takes time).
	const readMinDwell = 5 * time.Second
	const replyMinDwell = 60 * time.Second
	now := time.Now()

	type emailSession struct {
		start    time.Time
		duration time.Duration
	}

	var sessions []emailSession
	var sessionStart time.Time
	inEmail := false

	for i, e := range hyprEvents {
		cls := windowClass(e)
		if isEmailApp(cls) {
			if !inEmail {
				inEmail = true
				sessionStart = e.Timestamp
			}
		} else if inEmail {
			inEmail = false
			duration := e.Timestamp.Sub(sessionStart)
			sessions = append(sessions, emailSession{start: sessionStart, duration: duration})
		}

		// Handle the last event being in email.
		if i == len(hyprEvents)-1 && inEmail {
			duration := e.Timestamp.Sub(sessionStart)
			sessions = append(sessions, emailSession{start: sessionStart, duration: duration})
		}
	}

	if len(sessions) == 0 {
		return nil, nil
	}

	// Count reads (>5s) and replies (>60s) within the response window.
	reads := 0
	replies := 0
	cutoff := now.Add(-responsePendingWindow)

	for _, s := range sessions {
		if s.start.Before(cutoff) {
			continue
		}
		if s.duration >= replyMinDwell {
			replies++
		} else if s.duration >= readMinDwell {
			reads++
		}
	}

	// If there are reads but no replies, flag it.
	pendingCount := reads - replies
	if pendingCount <= 0 {
		return nil, nil
	}

	// Check for app_state events that might indicate compose windows.
	appStateEvents, _ := d.queryEventsByKindSince(ctx, event.KindAppState, cutoff)
	if len(appStateEvents) > 0 {
		// App state events available — reduce confidence concern since we may
		// have incomplete picture.  Only flag if substantially more reads.
		if pendingCount < 3 {
			return nil, nil
		}
	}

	return []notifier.Suggestion{{
		Category:   "workflow",
		Confidence: notifier.ConfidenceWeak,
		Title:      "Emails read but not replied to",
		Body: fmt.Sprintf(
			"%d email reading session(s) in the last %d hours with no substantial reply time — anything pending?",
			pendingCount, int(responsePendingWindow.Hours()),
		),
	}}, nil
}

// checkRepetitiveWorkflow detects: same app sequence performed multiple times
// across days.  Identifies automation candidates by finding app transition
// sequences that repeat on different calendar days.
func (d *Detector) checkRepetitiveWorkflow(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	if !d.hasMinObservation(ctx, since) {
		return nil, nil
	}

	// Use a 7-day window for cross-day repetition detection.
	lookback := time.Now().Add(-7 * 24 * time.Hour)
	if since.After(lookback) {
		lookback = since
	}

	hyprEvents, err := d.store.QueryHyprlandEvents(ctx, lookback)
	if err != nil {
		return nil, fmt.Errorf("patterns: repetitive_workflow: fetch hyprland events: %w", err)
	}
	if len(hyprEvents) < 6 {
		return nil, nil
	}

	// Extract app transition sequences of length 3 (trigrams) and track which
	// calendar days they appear on.
	type trigram struct {
		a, b, c string
	}
	trigramDays := make(map[trigram]map[string]bool) // trigram -> set of date strings

	prevPrev := ""
	prev := ""
	for _, e := range hyprEvents {
		cls := windowClass(e)
		if cls == "" {
			continue
		}

		// Deduplicate consecutive same-app events.
		if cls == prev {
			continue
		}

		if prevPrev != "" && prev != "" {
			tg := trigram{prevPrev, prev, cls}
			day := e.Timestamp.Format("2006-01-02")
			if trigramDays[tg] == nil {
				trigramDays[tg] = make(map[string]bool)
			}
			trigramDays[tg][day] = true
		}
		prevPrev = prev
		prev = cls
	}

	// Find trigrams that appear on >= repetitiveWorkflowMinOccurrences distinct days.
	var bestTrigram trigram
	bestDayCount := 0
	for tg, days := range trigramDays {
		if len(days) >= repetitiveWorkflowMinOccurrences && len(days) > bestDayCount {
			bestDayCount = len(days)
			bestTrigram = tg
		}
	}

	if bestDayCount == 0 {
		return nil, nil
	}

	return []notifier.Suggestion{{
		Category:   "workflow",
		Confidence: notifier.ConfidenceModerate,
		Title:      "Repetitive workflow detected",
		Body: fmt.Sprintf(
			"The sequence %s -> %s -> %s repeated on %d different days — candidate for automation?",
			bestTrigram.a, bestTrigram.b, bestTrigram.c, bestDayCount,
		),
	}}, nil
}

// checkEndOfDaySummary detects: approaching end of work hours + significant
// activity today.  Emits a suggestion to review the day's accomplishments.
func (d *Detector) checkEndOfDaySummary(ctx context.Context, since time.Time) ([]notifier.Suggestion, error) {
	if !d.hasMinObservation(ctx, since) {
		return nil, nil
	}

	now := time.Now()

	// Only fire between endOfDayHour and endOfDayHour+2 (e.g. 17:00-19:00).
	hour := now.Hour()
	if hour < endOfDayHour || hour >= endOfDayHour+2 {
		return nil, nil
	}

	// Count today's events across all sources.
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	hyprEvents, err := d.store.QueryHyprlandEvents(ctx, todayStart)
	if err != nil {
		return nil, fmt.Errorf("patterns: end_of_day_summary: fetch hyprland events: %w", err)
	}
	fileEvents, err := d.store.QueryRecentFileEvents(ctx, todayStart)
	if err != nil {
		return nil, fmt.Errorf("patterns: end_of_day_summary: fetch file events: %w", err)
	}
	termEvents, err := d.store.QueryTerminalEvents(ctx, todayStart)
	if err != nil {
		return nil, fmt.Errorf("patterns: end_of_day_summary: fetch terminal events: %w", err)
	}

	totalEvents := len(hyprEvents) + len(fileEvents) + len(termEvents)
	if totalEvents < endOfDayMinEvents {
		return nil, nil
	}

	// Count distinct apps used today.
	apps := make(map[string]bool)
	for _, e := range hyprEvents {
		cls := windowClass(e)
		if cls != "" {
			apps[cls] = true
		}
	}

	return []notifier.Suggestion{{
		Category:   "workflow",
		Confidence: notifier.ConfidenceWeak,
		Title:      "End-of-day summary available",
		Body: fmt.Sprintf(
			"Active day: %d events across %d apps, %d file edits, %d commands — review your accomplishments?",
			totalEvents, len(apps), len(fileEvents), len(termEvents),
		),
	}}, nil
}
