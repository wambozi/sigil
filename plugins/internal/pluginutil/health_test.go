package pluginutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthServer(t *testing.T) {
	h := NewHealthServer()

	// Initial status should be "ok".
	resp := getHealth(t, h)
	if resp["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", resp["status"])
	}
	if resp["error_count"] != float64(0) {
		t.Fatalf("expected 0 errors, got %v", resp["error_count"])
	}

	// RecordSuccess updates last_success.
	h.RecordSuccess()
	resp = getHealth(t, h)
	if resp["status"] != "ok" {
		t.Fatalf("expected status ok after success, got %v", resp["status"])
	}
	ls := resp["last_success"].(string)
	if ls == "0001-01-01T00:00:00Z" {
		t.Fatal("expected last_success to be updated")
	}

	// RecordError increments error count.
	h.RecordError()
	h.RecordError()
	resp = getHealth(t, h)
	if resp["error_count"] != float64(2) {
		t.Fatalf("expected 2 errors, got %v", resp["error_count"])
	}

	// SetDegraded changes status.
	h.SetDegraded()
	resp = getHealth(t, h)
	if resp["status"] != "degraded" {
		t.Fatalf("expected status degraded, got %v", resp["status"])
	}

	// RecordSuccess resets status to ok.
	h.RecordSuccess()
	resp = getHealth(t, h)
	if resp["status"] != "ok" {
		t.Fatalf("expected status ok after recovery, got %v", resp["status"])
	}
}

func getHealth(t *testing.T, h *HealthServer) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.handleHealth(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse health response: %v", err)
	}
	return resp
}
