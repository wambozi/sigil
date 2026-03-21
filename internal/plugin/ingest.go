package plugin

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
)

// EventHandler is called for each validated plugin event.
type EventHandler func(Event)

// IngestServer is a lightweight HTTP server that receives plugin events.
// It runs alongside the Unix socket server on a separate TCP port.
type IngestServer struct {
	mux     *http.ServeMux
	handler EventHandler
	log     *slog.Logger
}

// NewIngestServer creates an ingest server that calls handler for each event.
func NewIngestServer(handler EventHandler, log *slog.Logger) *IngestServer {
	s := &IngestServer{
		mux:     http.NewServeMux(),
		handler: handler,
		log:     log,
	}
	s.mux.HandleFunc("POST /api/v1/ingest", s.handleIngest)
	s.mux.HandleFunc("GET /health", s.handleHealth)
	return s
}

// Handler returns the HTTP handler for use with http.Server.
func (s *IngestServer) Handler() http.Handler {
	return s.mux
}

func (s *IngestServer) handleIngest(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Support both single events and batches.
	// Try array first, fall back to single object.
	var events []Event
	if err := json.Unmarshal(body, &events); err != nil {
		var single Event
		if err := json.Unmarshal(body, &single); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		events = []Event{single}
	}

	accepted := 0
	for _, e := range events {
		if e.Plugin == "" || e.Kind == "" {
			continue
		}
		s.handler(e)
		accepted++
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"accepted": accepted,
	})
}

func (s *IngestServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
