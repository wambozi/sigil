package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// RoutingPolicy defines centralized routing and model restrictions.
type RoutingPolicy struct {
	RoutingMode      string    `json:"routing_mode"`
	AllowedProviders []string  `json:"allowed_providers"`
	AllowedModelIDs  []string  `json:"allowed_model_ids"`
	EnforcedAt       time.Time `json:"enforced_at"`
}

// registerPolicyRoutes adds policy endpoints to the mux.
func (h *handlers) registerPolicyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/policy", h.handleGetPolicy)
	mux.HandleFunc("PUT /api/v1/policy", h.handleSetPolicy)
}

// handleGetPolicy returns the active policy for an org.
func (h *handlers) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	orgIDStr := r.URL.Query().Get("org_id")
	if orgIDStr == "" {
		orgIDStr = "1"
	}
	orgID, err := strconv.Atoi(orgIDStr)
	if err != nil {
		http.Error(w, "invalid org_id", http.StatusBadRequest)
		return
	}

	var policyJSON []byte
	var enforcedAt time.Time
	err = h.db.QueryRow(r.Context(),
		`SELECT policy, enforced_at FROM policies WHERE org_id = $1 ORDER BY enforced_at DESC LIMIT 1`,
		orgID,
	).Scan(&policyJSON, &enforcedAt)

	if err != nil {
		// No policy set — return empty/default
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(RoutingPolicy{
			RoutingMode: "localfirst",
			EnforcedAt:  time.Time{},
		})
		return
	}

	var policy RoutingPolicy
	if err := json.Unmarshal(policyJSON, &policy); err != nil {
		http.Error(w, "corrupt policy data", http.StatusInternalServerError)
		return
	}
	policy.EnforcedAt = enforcedAt

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(policy)
}

// handleSetPolicy stores a new policy for an org (admin only).
func (h *handlers) handleSetPolicy(w http.ResponseWriter, r *http.Request) {
	// Admin auth check — for now uses API key
	if h.apiKey != "" && r.Header.Get("X-API-Key") != h.apiKey {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		OrgID  int           `json:"org_id"`
		Policy RoutingPolicy `json:"policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.OrgID == 0 {
		req.OrgID = 1
	}

	policyJSON, err := json.Marshal(req.Policy)
	if err != nil {
		http.Error(w, "marshal policy", http.StatusInternalServerError)
		return
	}

	_, err = h.db.Exec(r.Context(),
		`INSERT INTO policies (org_id, policy, enforced_at) VALUES ($1, $2, NOW())`,
		req.OrgID, policyJSON,
	)
	if err != nil {
		h.log.Error("set policy", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
