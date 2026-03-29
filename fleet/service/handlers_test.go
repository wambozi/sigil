package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleHealthz(t *testing.T) {
	t.Parallel()

	h := &handlers{cloudCostPerQuery: 0.01}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	h.handleHealthz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %q", resp["status"])
	}
}

func TestHandleHealthzContentType(t *testing.T) {
	t.Parallel()

	h := &handlers{cloudCostPerQuery: 0.01}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	h.handleHealthz(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func TestHandleIngestReportUnauthorized(t *testing.T) {
	t.Parallel()

	h := &handlers{apiKey: "secret123", cloudCostPerQuery: 0.01}
	body := `{"node_id":"test-node","timestamp":"2026-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.handleIngestReport(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without API key, got %d", w.Code)
	}
}

func TestHandleIngestReportWrongAPIKey(t *testing.T) {
	t.Parallel()

	h := &handlers{apiKey: "correct-key", cloudCostPerQuery: 0.01}
	body := `{"node_id":"test-node","timestamp":"2026-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(body))
	req.Header.Set("X-API-Key", "wrong-key")
	w := httptest.NewRecorder()

	h.handleIngestReport(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong API key, got %d", w.Code)
	}
}

func TestHandleIngestReportBadBody(t *testing.T) {
	t.Parallel()

	h := &handlers{cloudCostPerQuery: 0.01}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader("not json"))
	w := httptest.NewRecorder()

	h.handleIngestReport(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", w.Code)
	}
}

func TestHandleIngestReportEmptyBody(t *testing.T) {
	t.Parallel()

	h := &handlers{cloudCostPerQuery: 0.01}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(""))
	w := httptest.NewRecorder()

	h.handleIngestReport(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty body, got %d", w.Code)
	}
}

func TestHandleIngestReportMissingNodeID(t *testing.T) {
	t.Parallel()

	h := &handlers{cloudCostPerQuery: 0.01}
	body := `{"timestamp":"2026-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.handleIngestReport(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing node_id, got %d", w.Code)
	}
}

func TestHandleIngestReportNoAPIKeyRequired(t *testing.T) {
	t.Parallel()

	// When apiKey is empty, auth should be skipped. The handler will panic on
	// the nil DB, so we recover and verify no 401 was written.
	h := &handlers{cloudCostPerQuery: 0.01}
	body := `{"node_id":"test-node","timestamp":"2026-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(body))
	w := httptest.NewRecorder()

	func() {
		defer func() { recover() }()
		h.handleIngestReport(w, req)
	}()

	// Should not be 401 — should get past auth (may panic on nil DB, which is fine).
	if w.Code == http.StatusUnauthorized {
		t.Error("expected to pass auth when apiKey is empty")
	}
}

func TestFleetReportJSONRoundTrip(t *testing.T) {
	t.Parallel()

	report := FleetReport{
		NodeID:               "node-abc-123",
		Timestamp:            time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC),
		AIQueryCounts:        map[string]int{"local": 50, "cloud": 10},
		SuggestionAcceptRate: 0.75,
		AdoptionTier:         3,
		LocalRoutingRatio:    0.85,
		BuildSuccessRate:     0.92,
		TotalEvents:          1500,
		TasksCompleted:       8,
		TasksStarted:         12,
		AvgTaskDurationMin:   45.5,
		StuckRate:            0.1,
		PhaseDistribution:    map[string]float64{"coding": 0.6, "review": 0.2, "testing": 0.2},
		AvgQualityScore:      85,
		QualityDegradations:  2,
		AvgSpeedScore:        7.5,
		MLEnabled:            true,
		MLPredictions:        100,
		MLRetrainCount:       3,
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal FleetReport: %v", err)
	}

	var decoded FleetReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal FleetReport: %v", err)
	}

	if decoded.NodeID != "node-abc-123" {
		t.Errorf("expected NodeID=node-abc-123, got %q", decoded.NodeID)
	}
	if decoded.AdoptionTier != 3 {
		t.Errorf("expected AdoptionTier=3, got %d", decoded.AdoptionTier)
	}
	if decoded.SuggestionAcceptRate != 0.75 {
		t.Errorf("expected SuggestionAcceptRate=0.75, got %f", decoded.SuggestionAcceptRate)
	}
	if decoded.BuildSuccessRate != 0.92 {
		t.Errorf("expected BuildSuccessRate=0.92, got %f", decoded.BuildSuccessRate)
	}
	if decoded.TotalEvents != 1500 {
		t.Errorf("expected TotalEvents=1500, got %d", decoded.TotalEvents)
	}
	if decoded.TasksCompleted != 8 {
		t.Errorf("expected TasksCompleted=8, got %d", decoded.TasksCompleted)
	}
	if decoded.MLEnabled != true {
		t.Error("expected MLEnabled=true")
	}
	if decoded.MLPredictions != 100 {
		t.Errorf("expected MLPredictions=100, got %d", decoded.MLPredictions)
	}
	if decoded.AIQueryCounts["local"] != 50 {
		t.Errorf("expected AIQueryCounts[local]=50, got %d", decoded.AIQueryCounts["local"])
	}
	if decoded.PhaseDistribution["coding"] != 0.6 {
		t.Errorf("expected PhaseDistribution[coding]=0.6, got %f", decoded.PhaseDistribution["coding"])
	}
}

func TestFleetReportJSONFieldNames(t *testing.T) {
	t.Parallel()

	report := FleetReport{
		NodeID:    "test-node",
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	expectedFields := []string{
		"node_id", "timestamp", "ai_query_counts", "suggestion_accept_rate",
		"adoption_tier", "local_routing_ratio", "build_success_rate",
		"total_events", "tasks_completed", "tasks_started",
		"avg_task_duration_min", "stuck_rate", "phase_distribution",
		"avg_quality_score", "quality_degradation_events",
		"avg_speed_score", "ml_enabled", "ml_predictions", "ml_retrain_count",
	}

	for _, field := range expectedFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("expected JSON field %q to be present", field)
		}
	}
}

func TestFleetReportMinimalValid(t *testing.T) {
	t.Parallel()

	// A minimal report with just node_id and timestamp should unmarshal fine.
	jsonStr := `{"node_id":"n1","timestamp":"2026-03-27T00:00:00Z"}`
	var report FleetReport
	if err := json.Unmarshal([]byte(jsonStr), &report); err != nil {
		t.Fatalf("unmarshal minimal report: %v", err)
	}
	if report.NodeID != "n1" {
		t.Errorf("expected NodeID=n1, got %q", report.NodeID)
	}
}

func TestFleetReportNilMaps(t *testing.T) {
	t.Parallel()

	// A report with nil maps should marshal without error.
	report := FleetReport{
		NodeID:    "test",
		Timestamp: time.Now(),
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report with nil maps: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	// nil maps become null in JSON.
	if decoded["ai_query_counts"] != nil {
		t.Errorf("expected null for nil AIQueryCounts map, got %v", decoded["ai_query_counts"])
	}
}

func TestHandleIngestReportArrayBody(t *testing.T) {
	t.Parallel()

	// An array body should fail to decode into FleetReport struct.
	h := &handlers{cloudCostPerQuery: 0.01}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(`[1,2,3]`))
	w := httptest.NewRecorder()

	h.handleIngestReport(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for array body, got %d", w.Code)
	}
}

func TestHandleIngestReportEmptyNodeID(t *testing.T) {
	t.Parallel()

	h := &handlers{cloudCostPerQuery: 0.01}
	body := `{"node_id":"","timestamp":"2026-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reports", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.handleIngestReport(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty node_id string, got %d", w.Code)
	}
}
