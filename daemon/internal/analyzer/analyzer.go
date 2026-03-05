// Package analyzer reads the local event store on a timer and sends
// summarised workflow context to Cactus for pattern analysis.
// It operates in two tiers:
//
//   - Local tier: statistical heuristics over the SQLite store (no network).
//   - Cloud tier: periodically sends a context summary to Cactus for deeper
//     reasoning.  Cactus decides whether to handle it on-device or in the
//     cloud based on the configured routing mode.
package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wambozi/aether/internal/cactus"
	"github.com/wambozi/aether/internal/event"
	"github.com/wambozi/aether/internal/store"
)

// Summary is a structured digest produced by the local heuristic tier and
// optionally enriched by the Cactus cloud tier.
type Summary struct {
	Period        time.Duration
	EventCounts   map[event.Kind]int64
	TopFiles      []string
	Insights      string // LLM-generated narrative (may be empty)
	CactusRouting string // "local" | "cloud" | "" (not yet queried)
	GeneratedAt   time.Time
}

// Analyzer drives both the local heuristic pass and the periodic Cactus query.
type Analyzer struct {
	store    *store.Store
	cactus   *cactus.Client
	interval time.Duration
	log      *slog.Logger

	// OnSummary is called every time a new summary is produced.
	// It runs in the analyzer goroutine — implementations must be non-blocking
	// or hand off to another goroutine.
	OnSummary func(Summary)
}

// New creates an Analyzer.  interval is how often a full analysis cycle runs
// (the product plan specifies hourly for v0).
func New(s *store.Store, c *cactus.Client, interval time.Duration, log *slog.Logger) *Analyzer {
	return &Analyzer{
		store:    s,
		cactus:   c,
		interval: interval,
		log:      log,
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

	// Cloud pass — send a context digest to Cactus.
	if a.cactus != nil {
		if err := a.cloudPass(ctx, &summary); err != nil {
			// Non-fatal: local summary is still useful without LLM enrichment.
			a.log.Warn("analyzer: cloud pass", "err", err)
		}
	}

	summary.GeneratedAt = time.Now()
	a.log.Info("analyzer: cycle complete",
		"insights_chars", len(summary.Insights),
		"routing", summary.CactusRouting,
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

	return summary, nil
}

// cloudPass sends a prose summary of recent activity to Cactus and stores the
// LLM's response in the summary.Insights field.
func (a *Analyzer) cloudPass(ctx context.Context, s *Summary) error {
	userPrompt := buildPrompt(s)

	result, err := a.cactus.Complete(ctx,
		systemPrompt,
		userPrompt,
	)
	if err != nil {
		return err
	}

	s.Insights = result.Content
	s.CactusRouting = result.Routing

	// Persist the AI interaction so fleet metrics can aggregate it.
	_ = a.store.InsertAIInteraction(ctx, event.AIInteraction{
		QueryCategory: "workflow_analysis",
		Routing:       result.Routing,
		LatencyMS:     result.LatencyMS,
		Timestamp:     time.Now(),
	})

	return nil
}

const systemPrompt = `You are the intelligence layer of Aether OS — a self-tuning
operating system for professional software engineers. You receive a summary of
recent workflow activity collected on the engineer's machine.

Your task: identify patterns, surface one or two actionable insights, and flag
anything that looks like the engineer might be blocked or struggling. Be concise
(3–5 sentences maximum). Do not include pleasantries. Do not hallucinate specific
file paths or commands — only infer from the data given.`

// buildPrompt converts a local Summary into a text prompt for the LLM.
func buildPrompt(s *Summary) string {
	return fmt.Sprintf(
		"Workflow summary for the past %s:\n"+
			"- File system events: %d\n"+
			"- Process events: %d\n"+
			"- Git events: %d\n"+
			"- Hyprland (window/workspace) events: %d\n"+
			"- AI interaction events: %d\n\n"+
			"What patterns do you notice? What might help this engineer?",
		s.Period,
		s.EventCounts[event.KindFile],
		s.EventCounts[event.KindProcess],
		s.EventCounts[event.KindGit],
		s.EventCounts[event.KindHyprland],
		s.EventCounts[event.KindAI],
	)
}
