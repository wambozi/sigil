// Package fleet implements the Fleet Reporter subsystem that computes and
// sends anonymized aggregate metrics to a Fleet Aggregation Layer.
package fleet

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wambozi/aether/internal/config"
	"github.com/wambozi/aether/internal/event"
	"github.com/wambozi/aether/internal/store"
)

// FleetReport is the anonymized aggregate payload sent to the Fleet Aggregation Layer.
type FleetReport struct {
	NodeID               string         `json:"node_id"`
	Timestamp            time.Time      `json:"timestamp"`
	AIQueryCounts        map[string]int `json:"ai_query_counts"`
	SuggestionAcceptRate float64        `json:"suggestion_accept_rate"`
	AdoptionTier         int            `json:"adoption_tier"` // 0-3
	CactusRoutingRatio   float64        `json:"cactus_routing_ratio"`
	BuildSuccessRate     float64        `json:"build_success_rate"`
	TotalEvents          int            `json:"total_events"`
}

// RoutingPolicy defines centralized routing and model restrictions
// fetched from the Fleet Aggregation Layer.
type RoutingPolicy struct {
	RoutingMode      string   `json:"routing_mode"`
	AllowedProviders []string `json:"allowed_providers"`
	AllowedModelIDs  []string `json:"allowed_model_ids"`
	EnforcedAt       time.Time `json:"enforced_at"`
}

// Reporter computes and sends anonymized aggregate metrics to the Fleet Aggregation Layer.
type Reporter struct {
	store    *store.Store
	endpoint string
	enabled  bool
	nodeID   string
	interval time.Duration
	log      *slog.Logger

	mu     sync.Mutex
	queue  []FleetReport
	policy *RoutingPolicy
}

// New creates a Reporter from the given config.
func New(s *store.Store, cfg config.FleetConfig, log *slog.Logger) *Reporter {
	interval := time.Hour
	if cfg.Interval != "" {
		if d, err := time.ParseDuration(cfg.Interval); err == nil && d > 0 {
			interval = d
		}
	}

	nodeID := cfg.NodeID
	if nodeID == "" {
		nodeID = loadOrCreateNodeID(log)
	}

	return &Reporter{
		store:    s,
		endpoint: cfg.Endpoint,
		enabled:  cfg.Enabled,
		nodeID:   nodeID,
		interval: interval,
		log:      log,
	}
}

// Run computes and sends reports on the configured interval until ctx is cancelled.
func (r *Reporter) Run(ctx context.Context) {
	if !r.enabled {
		r.log.Info("fleet reporter disabled")
		return
	}

	r.log.Info("fleet reporter started", "endpoint", r.endpoint, "interval", r.interval)

	// Fetch policy at startup
	r.fetchPolicy(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.cycle(ctx)
			r.fetchPolicy(ctx)
		}
	}
}

// cycle computes a report and attempts to send all queued reports.
func (r *Reporter) cycle(ctx context.Context) {
	report, err := r.computeReport(ctx)
	if err != nil {
		r.log.Error("fleet reporter: compute report", "err", err)
		return
	}

	r.mu.Lock()
	r.queue = append(r.queue, report)
	r.mu.Unlock()

	r.sendQueued(ctx)
}

// Preview returns the current FleetReport without sending it.
func (r *Reporter) Preview(ctx context.Context) (FleetReport, error) {
	return r.computeReport(ctx)
}

// OptOut clears the pending queue and disables reporting.
func (r *Reporter) OptOut() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queue = nil
	r.enabled = false
	r.log.Info("fleet reporter: opted out")
}

// CurrentPolicy returns the last fetched routing policy, or nil if none.
func (r *Reporter) CurrentPolicy() *RoutingPolicy {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.policy
}

// fetchPolicy retrieves the routing policy from the fleet endpoint.
func (r *Reporter) fetchPolicy(ctx context.Context) {
	if r.endpoint == "" {
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		r.endpoint+"/api/v1/policy", nil)
	if err != nil {
		r.log.Warn("fleet reporter: create policy request", "err", err)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.log.Warn("fleet reporter: fetch policy", "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		r.log.Warn("fleet reporter: policy endpoint returned", "status", resp.StatusCode)
		return
	}

	var policy RoutingPolicy
	if err := json.NewDecoder(resp.Body).Decode(&policy); err != nil {
		r.log.Warn("fleet reporter: decode policy", "err", err)
		return
	}

	r.mu.Lock()
	r.policy = &policy
	r.mu.Unlock()
	r.log.Info("fleet reporter: policy updated", "routing_mode", policy.RoutingMode)
}

// Enabled returns whether fleet reporting is currently enabled.
func (r *Reporter) Enabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enabled
}

// QueueSize returns the number of pending reports.
func (r *Reporter) QueueSize() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.queue)
}

// computeReport builds a FleetReport from store queries.
func (r *Reporter) computeReport(ctx context.Context) (FleetReport, error) {
	since := time.Now().Add(-r.interval)

	report := FleetReport{
		NodeID:        r.nodeID,
		Timestamp:     time.Now(),
		AIQueryCounts: make(map[string]int),
	}

	// AI query counts by category
	aiInteractions, err := r.store.QueryAIInteractions(ctx, since)
	if err != nil {
		return report, fmt.Errorf("query ai interactions: %w", err)
	}

	var localQueries, totalQueries int
	for _, ai := range aiInteractions {
		cat := ai.QueryCategory
		if cat == "" {
			cat = "uncategorized"
		}
		report.AIQueryCounts[cat]++
		totalQueries++
		if ai.Routing == "local" {
			localQueries++
		}
	}

	// Cactus routing ratio (local / total)
	if totalQueries > 0 {
		report.CactusRoutingRatio = float64(localQueries) / float64(totalQueries)
	}

	// Suggestion acceptance rate
	rate, err := r.store.QuerySuggestionAcceptanceRate(ctx, since)
	if err != nil {
		r.log.Warn("fleet reporter: query acceptance rate", "err", err)
	} else {
		report.SuggestionAcceptRate = rate
	}

	// Adoption tier detection
	report.AdoptionTier = detectAdoptionTier(totalQueries, rate)

	// Total events across all kinds
	var total int64
	for _, kind := range []event.Kind{
		event.KindFile, event.KindProcess, event.KindGit, event.KindTerminal, event.KindAI,
	} {
		n, err := r.store.CountEvents(ctx, kind, since)
		if err != nil {
			continue
		}
		total += n
	}
	report.TotalEvents = int(total)

	// Build success rate from terminal events
	report.BuildSuccessRate = r.computeBuildSuccessRate(ctx, since)

	return report, nil
}

// detectAdoptionTier classifies into tiers 0-3 based on AI usage signals.
//   - 0 (Observer): no AI queries
//   - 1 (Explorer): some AI queries, low acceptance
//   - 2 (Integrator): regular AI queries, moderate acceptance
//   - 3 (Native): heavy AI queries, high acceptance
func detectAdoptionTier(queryCount int, acceptRate float64) int {
	switch {
	case queryCount == 0:
		return 0
	case queryCount < 5 || acceptRate < 0.2:
		return 1
	case queryCount < 20 || acceptRate < 0.5:
		return 2
	default:
		return 3
	}
}

// computeBuildSuccessRate checks terminal events for build/test commands
// and returns the fraction that exited with code 0.
func (r *Reporter) computeBuildSuccessRate(ctx context.Context, since time.Time) float64 {
	events, err := r.store.QueryTerminalEvents(ctx, since)
	if err != nil {
		return 0
	}

	var builds, successes int
	for _, e := range events {
		cmd, _ := e.Payload["cmd"].(string)
		if !isBuildCommand(cmd) {
			continue
		}
		builds++
		exitCode := 0
		if v, ok := e.Payload["exit_code"]; ok {
			switch ec := v.(type) {
			case float64:
				exitCode = int(ec)
			case int:
				exitCode = ec
			}
		}
		if exitCode == 0 {
			successes++
		}
	}

	if builds == 0 {
		return 0
	}
	return float64(successes) / float64(builds)
}

// isBuildCommand returns true if the command looks like a build or test invocation.
func isBuildCommand(cmd string) bool {
	prefixes := []string{
		"go build", "go test", "go vet",
		"make", "cargo build", "cargo test",
		"npm run build", "npm test", "npm run test",
		"yarn build", "yarn test",
		"gradle", "mvn",
	}
	for _, p := range prefixes {
		if len(cmd) >= len(p) && cmd[:len(p)] == p {
			return true
		}
	}
	return false
}

// sendQueued attempts to POST each queued report to the fleet endpoint.
func (r *Reporter) sendQueued(ctx context.Context) {
	r.mu.Lock()
	if len(r.queue) == 0 {
		r.mu.Unlock()
		return
	}
	pending := make([]FleetReport, len(r.queue))
	copy(pending, r.queue)
	r.mu.Unlock()

	var sent int
	for _, report := range pending {
		if err := r.postReport(ctx, report); err != nil {
			r.log.Warn("fleet reporter: send failed, will retry", "err", err)
			break
		}
		sent++
	}

	if sent > 0 {
		r.mu.Lock()
		r.queue = r.queue[sent:]
		r.mu.Unlock()
		r.log.Info("fleet reporter: sent reports", "count", sent)
	}
}

// postReport sends a single report to the fleet endpoint.
func (r *Reporter) postReport(ctx context.Context, report FleetReport) error {
	if r.endpoint == "" {
		return fmt.Errorf("no fleet endpoint configured")
	}

	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.endpoint+"/api/v1/reports", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post report: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("fleet endpoint returned %d", resp.StatusCode)
	}
	return nil
}

// loadOrCreateNodeID reads or generates a stable anonymous node ID.
// The ID is persisted to ~/.local/share/aetherd/node_id.
func loadOrCreateNodeID(log *slog.Logger) string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".local", "share")
	}
	dir := filepath.Join(base, "aetherd")
	path := filepath.Join(dir, "node_id")

	data, err := os.ReadFile(path)
	if err == nil && len(data) > 0 {
		return string(bytes.TrimSpace(data))
	}

	id := generateUUID()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Warn("fleet reporter: create node_id dir", "err", err)
		return id
	}
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		log.Warn("fleet reporter: write node_id", "err", err)
	}
	return id
}

// generateUUID returns a random UUID v4 string.
func generateUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 1
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
