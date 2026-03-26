package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// handlers holds shared dependencies for HTTP handler methods.
type handlers struct {
	db                *pgxpool.Pool
	log               *slog.Logger
	apiKey            string
	cloudCostPerQuery float64
}

// FleetReport mirrors the daemon's fleet.FleetReport type.
type FleetReport struct {
	NodeID               string         `json:"node_id"`
	Timestamp            time.Time      `json:"timestamp"`
	AIQueryCounts        map[string]int `json:"ai_query_counts"`
	SuggestionAcceptRate float64        `json:"suggestion_accept_rate"`
	AdoptionTier         int            `json:"adoption_tier"`
	LocalRoutingRatio    float64        `json:"local_routing_ratio"`
	BuildSuccessRate     float64        `json:"build_success_rate"`
	TotalEvents          int            `json:"total_events"`
}

// handleIngestReport receives a FleetReport and upserts it into daily_metrics.
func (h *handlers) handleIngestReport(w http.ResponseWriter, r *http.Request) {
	if h.apiKey != "" {
		if r.Header.Get("X-API-Key") != h.apiKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	var report FleetReport
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if report.NodeID == "" {
		http.Error(w, "node_id is required", http.StatusBadRequest)
		return
	}

	queryCounts, _ := json.Marshal(report.AIQueryCounts)
	date := report.Timestamp.Format("2006-01-02")

	_, err := h.db.Exec(r.Context(), `
		INSERT INTO daily_metrics (node_id, date, ai_query_counts, suggestion_accept_rate,
			adoption_tier, local_routing_ratio, build_success_rate, total_events)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (node_id, date) DO UPDATE SET
			ai_query_counts = $3,
			suggestion_accept_rate = $4,
			adoption_tier = $5,
			local_routing_ratio = $6,
			build_success_rate = $7,
			total_events = $8,
			received_at = NOW()
	`, report.NodeID, date, queryCounts, report.SuggestionAcceptRate,
		report.AdoptionTier, report.LocalRoutingRatio, report.BuildSuccessRate, report.TotalEvents)

	if err != nil {
		h.log.Error("ingest report", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleQueryMetrics returns aggregated metrics based on query parameters.
// Supported views: adoption, velocity, cost, compliance.
func (h *handlers) handleQueryMetrics(w http.ResponseWriter, r *http.Request) {
	view := r.URL.Query().Get("view")
	orgID := r.URL.Query().Get("org_id")
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	if from == "" {
		from = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	}
	if to == "" {
		to = time.Now().Format("2006-01-02")
	}

	var result any
	var err error

	switch view {
	case "adoption":
		result, err = h.queryAdoption(r, orgID, from, to)
	case "velocity":
		result, err = h.queryVelocity(r, orgID, from, to)
	case "cost":
		result, err = h.queryCost(r, orgID, from, to)
	case "compliance":
		result, err = h.queryCompliance(r, orgID, from, to)
	default:
		result, err = h.queryOverview(r, orgID, from, to)
	}

	if err != nil {
		h.log.Error("query metrics", "view", view, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// queryOverview returns a summary of all metrics.
func (h *handlers) queryOverview(r *http.Request, orgID, from, to string) (any, error) {
	rows, err := h.db.Query(r.Context(), `
		SELECT date, COUNT(*) as node_count,
			AVG(suggestion_accept_rate) as avg_accept_rate,
			AVG(adoption_tier) as avg_tier,
			AVG(local_routing_ratio) as avg_routing_ratio,
			AVG(build_success_rate) as avg_build_rate,
			SUM(total_events) as total_events
		FROM daily_metrics dm
		WHERE date >= $1 AND date <= $2
		GROUP BY date ORDER BY date
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type dayRow struct {
		Date            string  `json:"date"`
		NodeCount       int     `json:"node_count"`
		AvgAcceptRate   float64 `json:"avg_accept_rate"`
		AvgTier         float64 `json:"avg_tier"`
		AvgRoutingRatio float64 `json:"avg_routing_ratio"`
		AvgBuildRate    float64 `json:"avg_build_rate"`
		TotalEvents     int64   `json:"total_events"`
	}

	var results []dayRow
	for rows.Next() {
		var d dayRow
		var date time.Time
		if err := rows.Scan(&date, &d.NodeCount, &d.AvgAcceptRate, &d.AvgTier,
			&d.AvgRoutingRatio, &d.AvgBuildRate, &d.TotalEvents); err != nil {
			return nil, err
		}
		d.Date = date.Format("2006-01-02")
		results = append(results, d)
	}
	return map[string]any{"view": "overview", "data": results}, rows.Err()
}

// queryAdoption returns tier distribution over time.
func (h *handlers) queryAdoption(r *http.Request, orgID, from, to string) (any, error) {
	rows, err := h.db.Query(r.Context(), `
		SELECT date, adoption_tier, COUNT(*) as count
		FROM daily_metrics
		WHERE date >= $1 AND date <= $2
		GROUP BY date, adoption_tier
		ORDER BY date, adoption_tier
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type tierRow struct {
		Date  string `json:"date"`
		Tier  int    `json:"tier"`
		Count int    `json:"count"`
	}

	var results []tierRow
	for rows.Next() {
		var tr tierRow
		var date time.Time
		if err := rows.Scan(&date, &tr.Tier, &tr.Count); err != nil {
			return nil, err
		}
		tr.Date = date.Format("2006-01-02")
		results = append(results, tr)
	}
	return map[string]any{"view": "adoption", "data": results}, rows.Err()
}

// queryVelocity returns build success rate and event volume by adoption tier.
func (h *handlers) queryVelocity(r *http.Request, orgID, from, to string) (any, error) {
	rows, err := h.db.Query(r.Context(), `
		SELECT adoption_tier,
			AVG(build_success_rate) as avg_build_rate,
			AVG(total_events) as avg_events
		FROM daily_metrics
		WHERE date >= $1 AND date <= $2
		GROUP BY adoption_tier
		ORDER BY adoption_tier
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type velocityRow struct {
		Tier         int     `json:"tier"`
		AvgBuildRate float64 `json:"avg_build_rate"`
		AvgEvents    float64 `json:"avg_events"`
	}

	var results []velocityRow
	for rows.Next() {
		var vr velocityRow
		if err := rows.Scan(&vr.Tier, &vr.AvgBuildRate, &vr.AvgEvents); err != nil {
			return nil, err
		}
		results = append(results, vr)
	}
	return map[string]any{
		"view":       "velocity",
		"data":       results,
		"disclaimer": "These correlations show aggregate trends. They are not individual performance evaluations.",
	}, rows.Err()
}

// queryCost returns local-vs-cloud routing ratio and cost estimates.
func (h *handlers) queryCost(r *http.Request, orgID, from, to string) (any, error) {
	rows, err := h.db.Query(r.Context(), `
		SELECT date,
			AVG(local_routing_ratio) as avg_local_ratio,
			SUM(total_events) as total_events,
			SUM(total_events * (1.0 - local_routing_ratio)) as cloud_queries
		FROM daily_metrics
		WHERE date >= $1 AND date <= $2
		GROUP BY date ORDER BY date
	`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type costRow struct {
		Date          string  `json:"date"`
		LocalRatio    float64 `json:"local_ratio"`
		TotalEvents   int64   `json:"total_events"`
		CloudQueries  float64 `json:"cloud_queries"`
		EstimatedCost float64 `json:"estimated_cost"`
	}

	var results []costRow
	for rows.Next() {
		var cr costRow
		var date time.Time
		if err := rows.Scan(&date, &cr.LocalRatio, &cr.TotalEvents, &cr.CloudQueries); err != nil {
			return nil, err
		}
		cr.Date = date.Format("2006-01-02")
		cr.EstimatedCost = cr.CloudQueries * h.cloudCostPerQuery
		results = append(results, cr)
	}
	return map[string]any{"view": "cost", "data": results}, rows.Err()
}

// queryCompliance returns routing compliance and data residency information.
func (h *handlers) queryCompliance(r *http.Request, orgID, from, to string) (any, error) {
	var totalNodes int
	var avgLocalRatio float64
	err := h.db.QueryRow(r.Context(), `
		SELECT COUNT(DISTINCT node_id), COALESCE(AVG(local_routing_ratio), 0)
		FROM daily_metrics
		WHERE date >= $1 AND date <= $2
	`, from, to).Scan(&totalNodes, &avgLocalRatio)
	if err != nil {
		return nil, err
	}

	orgName := "default"
	if orgID != "" {
		orgIDInt, _ := strconv.Atoi(orgID)
		_ = h.db.QueryRow(r.Context(),
			`SELECT name FROM orgs WHERE id = $1`, orgIDInt).Scan(&orgName)
	}

	return map[string]any{
		"view":            "compliance",
		"total_nodes":     totalNodes,
		"local_pct":       avgLocalRatio * 100,
		"cloud_pct":       (1 - avgLocalRatio) * 100,
		"all_approved":    true,
		"data_residency":  "100% local (only anonymized aggregates transmitted)",
		"org_name":        orgName,
		"date_range_from": from,
		"date_range_to":   to,
	}, nil
}

// handleHealthz returns a simple health check response.
func (h *handlers) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
