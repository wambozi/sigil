// Package analyzer reads the local event store on a timer and sends
// summarised workflow context to the inference engine for pattern analysis.
// It operates in two tiers:
//
//   - Local tier: statistical heuristics over the SQLite store (no network).
//   - Cloud tier: periodically sends a context summary to the inference engine
//     for deeper reasoning.  The engine decides whether to handle it on-device
//     or in the cloud based on the configured routing mode.
package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/inference"
	"github.com/wambozi/sigil/internal/notifier"
	"github.com/wambozi/sigil/internal/store"
)

// Summary is a structured digest produced by the local heuristic tier and
// optionally enriched by the inference engine.
type Summary struct {
	Period           time.Duration
	EventCounts      map[event.Kind]int64
	TopFiles         []store.FileEditCount
	Insights         string // LLM-generated narrative (may be empty)
	InferenceRouting string // "local" | "cloud" | "" (not yet queried)
	GeneratedAt      time.Time

	// AcceptanceRate is the ratio of accepted/(accepted+dismissed) suggestions.
	AcceptanceRate float64

	// AIInteractions holds the last 20 AI interaction records in the window.
	AIInteractions []event.AIInteraction

	// Suggestions are pattern-based insights produced by the local heuristic
	// detector.  Callers should surface each one via notifier.Surface.
	Suggestions []notifier.Suggestion
}

// Analyzer drives both the local heuristic pass and the periodic inference query.
type Analyzer struct {
	store    *store.Store
	engine   *inference.Engine
	detector *Detector
	interval  time.Duration
	log       *slog.Logger
	triggerCh chan struct{}

	// OnSummary is called every time a new summary is produced.
	// It runs in the analyzer goroutine — implementations must be non-blocking
	// or hand off to another goroutine.
	OnSummary func(Summary)
}

// New creates an Analyzer.  interval is how often a full analysis cycle runs
// (the product plan specifies hourly for v0).
func New(s *store.Store, engine *inference.Engine, interval time.Duration, log *slog.Logger) *Analyzer {
	return &Analyzer{
		store:  s,
		engine: engine,
		detector:  NewDetector(s, log),
		interval:  interval,
		log:       log,
		triggerCh: make(chan struct{}, 1),
	}
}

// Trigger requests an immediate analysis cycle outside the normal interval.
// Non-blocking: if a trigger is already pending, the call is a no-op.
func (a *Analyzer) Trigger() {
	select {
	case a.triggerCh <- struct{}{}:
	default:
		// A trigger is already queued; no need to queue another.
	}
}

// Run starts the analysis loop and blocks until ctx is cancelled.
func (a *Analyzer) Run(ctx context.Context) {
	// Kick off an initial pass immediately so the engineer gets feedback fast.
	a.runCycle(ctx)

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.runCycle(ctx)
		case <-a.triggerCh:
			a.log.Info("analyzer: manual trigger received")
			a.runCycle(ctx)
		}
	}
}

// runCycle executes one full local + cloud analysis cycle.
func (a *Analyzer) runCycle(ctx context.Context) {
	a.log.Info("analyzer: starting cycle")

	summary, err := a.localPass(ctx)
	if err != nil {
		a.log.Error("analyzer: local pass", "err", err)
		return
	}

	// Cloud pass — ping the inference engine before each attempt so the daemon
	// reconnects automatically if it was unavailable at startup or went down.
	if a.engine != nil {
		pingCtx, cancelPing := context.WithTimeout(ctx, 5*time.Second)
		pingErr := a.engine.Ping(pingCtx)
		cancelPing()
		if pingErr != nil {
			a.log.Warn("analyzer: inference engine unreachable — skipping cloud pass", "err", pingErr)
		} else if err := a.cloudPass(ctx, &summary); err != nil {
			// Non-fatal: local summary is still useful without LLM enrichment.
			a.log.Warn("analyzer: cloud pass", "err", err)
		}
	}

	summary.GeneratedAt = time.Now()
	a.log.Info("analyzer: cycle complete",
		"insights_chars", len(summary.Insights),
		"routing", summary.InferenceRouting,
	)

	if a.OnSummary != nil {
		a.OnSummary(summary)
	}
}

// localPass computes heuristic metrics from the store — no network required.
func (a *Analyzer) localPass(ctx context.Context) (Summary, error) {
	since := time.Now().Add(-a.interval)
	summary := Summary{
		Period:      a.interval,
		EventCounts: make(map[event.Kind]int64),
	}

	for _, k := range []event.Kind{
		event.KindFile, event.KindProcess,
		event.KindHyprland, event.KindGit, event.KindAI,
	} {
		n, err := a.store.CountEvents(ctx, k, since)
		if err != nil {
			return summary, fmt.Errorf("count %s events: %w", k, err)
		}
		summary.EventCounts[k] = n
	}

	suggestions, err := a.detector.Detect(ctx, a.interval)
	if err != nil {
		// Non-fatal: pattern detection failures should not suppress the summary.
		a.log.Warn("analyzer: pattern detection", "err", err)
	}
	summary.Suggestions = suggestions

	// Populate enrichment fields for the LLM prompt.
	topFiles, err := a.store.QueryTopFiles(ctx, since, 5)
	if err != nil {
		a.log.Warn("analyzer: query top files", "err", err)
	} else {
		summary.TopFiles = topFiles
	}

	rate, err := a.store.QuerySuggestionAcceptanceRate(ctx, since)
	if err != nil {
		a.log.Warn("analyzer: query acceptance rate", "err", err)
	} else {
		summary.AcceptanceRate = rate
	}

	aiInteractions, err := a.store.QueryAIInteractions(ctx, since)
	if err != nil {
		a.log.Warn("analyzer: query AI interactions", "err", err)
	} else {
		// Keep only the last 20.
		if len(aiInteractions) > 20 {
			aiInteractions = aiInteractions[len(aiInteractions)-20:]
		}
		summary.AIInteractions = aiInteractions
	}

	return summary, nil
}

// cloudPass sends a prose summary of recent activity to the inference engine
// and stores the LLM's response in the summary.Insights field.
func (a *Analyzer) cloudPass(ctx context.Context, s *Summary) error {
	userPrompt := buildPrompt(s)

	result, err := a.engine.Complete(ctx,
		systemPrompt,
		userPrompt,
	)
	if err != nil {
		return err
	}

	s.Insights = result.Content
	s.InferenceRouting = result.Routing

	// Persist the AI interaction so fleet metrics can aggregate it.
	_ = a.store.InsertAIInteraction(ctx, event.AIInteraction{
		QueryCategory: "workflow_analysis",
		Routing:       result.Routing,
		LatencyMS:     result.LatencyMS,
		Timestamp:     time.Now(),
	})

	return nil
}

const systemPrompt = `You are the intelligence layer of Sigil OS — a self-tuning
operating system for professional software engineers. You receive a summary of
recent workflow activity collected on the engineer's machine.

Your task: identify patterns, surface one or two actionable insights, and flag
anything that looks like the engineer might be blocked or struggling. Be concise
(3–5 sentences maximum). Do not include pleasantries. Do not hallucinate specific
file paths or commands — only infer from the data given.`

// buildPrompt converts a local Summary into a text prompt for the LLM.
// Stays under 7500 characters; truncates patterns and files if needed.
func buildPrompt(s *Summary) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Workflow summary for the past %s:\n", s.Period)
	fmt.Fprintf(&b, "Events: %d file, %d process, %d git, %d terminal, %d AI interactions\n",
		s.EventCounts[event.KindFile],
		s.EventCounts[event.KindProcess],
		s.EventCounts[event.KindGit],
		s.EventCounts[event.KindTerminal],
		s.EventCounts[event.KindAI],
	)

	// Top edited files.
	files := s.TopFiles
	if len(files) > 0 {
		b.WriteString("\nTop edited files: ")
		for i, f := range files {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s (%d edits)", f.Path, f.Count)
		}
		b.WriteString("\n")
	}

	// Detected patterns.
	suggestions := s.Suggestions
	if len(suggestions) > 0 {
		b.WriteString("\nDetected patterns:\n")
		for _, sg := range suggestions {
			fmt.Fprintf(&b, "- %s: %s\n", sg.Title, sg.Body)
		}
	}

	// AI usage summary.
	if len(s.AIInteractions) > 0 {
		catCounts := make(map[string]int)
		for _, ai := range s.AIInteractions {
			if ai.QueryCategory != "" {
				catCounts[ai.QueryCategory]++
			}
		}
		topCat := ""
		topN := 0
		for cat, n := range catCounts {
			if n > topN {
				topN = n
				topCat = cat
			}
		}
		fmt.Fprintf(&b, "\nAI usage: %d queries in this window, %.0f%% suggestion acceptance rate.",
			len(s.AIInteractions), s.AcceptanceRate*100)
		if topCat != "" {
			fmt.Fprintf(&b, "\nMost common query category: %s.", topCat)
		}
		b.WriteString("\n")
	}

	// Build health from terminal events in suggestions.
	var buildCmds, buildSuccesses int
	for _, sg := range suggestions {
		if strings.Contains(sg.Title, "build/test failures") {
			buildCmds++
		}
	}
	if buildCmds > 0 || s.EventCounts[event.KindTerminal] > 0 {
		fmt.Fprintf(&b, "\nBuild health: %d terminal events observed.\n",
			s.EventCounts[event.KindTerminal])
		_ = buildSuccesses // detailed build stats require terminal event scan
	}

	b.WriteString("\nWhat patterns do you notice? What might help this engineer?")

	prompt := b.String()

	// Token guard: truncate if over 7500 characters.
	if len(prompt) > 7500 {
		b.Reset()
		fmt.Fprintf(&b, "Workflow summary for the past %s:\n", s.Period)
		fmt.Fprintf(&b, "Events: %d file, %d process, %d git, %d terminal, %d AI interactions\n",
			s.EventCounts[event.KindFile],
			s.EventCounts[event.KindProcess],
			s.EventCounts[event.KindGit],
			s.EventCounts[event.KindTerminal],
			s.EventCounts[event.KindAI],
		)

		if len(files) > 3 {
			files = files[:3]
		}
		if len(files) > 0 {
			b.WriteString("\nTop edited files: ")
			for i, f := range files {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "%s (%d edits)", f.Path, f.Count)
			}
			b.WriteString("\n")
		}

		truncSuggestions := suggestions
		if len(truncSuggestions) > 3 {
			truncSuggestions = truncSuggestions[:3]
		}
		if len(truncSuggestions) > 0 {
			b.WriteString("\nDetected patterns:\n")
			for _, sg := range truncSuggestions {
				fmt.Fprintf(&b, "- %s: %s\n", sg.Title, sg.Body)
			}
		}

		b.WriteString("\nWhat patterns do you notice? What might help this engineer?")
		prompt = b.String()
	}

	return prompt
}
