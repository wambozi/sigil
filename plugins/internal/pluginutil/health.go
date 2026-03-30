package pluginutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// HealthServer provides a /health endpoint for plugin health monitoring.
type HealthServer struct {
	lastSuccess atomic.Value // time.Time
	errorCount  atomic.Int64
	status      atomic.Value // string: "ok", "degraded", "error"
}

// NewHealthServer creates a HealthServer with initial "ok" status.
func NewHealthServer() *HealthServer {
	h := &HealthServer{}
	h.status.Store("ok")
	h.lastSuccess.Store(time.Time{})
	return h
}

// RecordSuccess marks a successful operation and resets status to "ok".
func (h *HealthServer) RecordSuccess() {
	h.lastSuccess.Store(time.Now())
	h.status.Store("ok")
}

// RecordError increments the error counter.
func (h *HealthServer) RecordError() {
	h.errorCount.Add(1)
}

// SetDegraded marks the plugin as degraded (e.g. auth expired, rate limited).
func (h *HealthServer) SetDegraded() {
	h.status.Store("degraded")
}

// ServeHealth starts an HTTP server on the given port with a /health endpoint.
func (h *HealthServer) ServeHealth(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	return http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), mux)
}

func (h *HealthServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	ls, _ := h.lastSuccess.Load().(time.Time)
	resp := map[string]any{
		"status":       h.status.Load(),
		"error_count":  h.errorCount.Load(),
		"last_success": ls.Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
