package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// AuthConfig holds OIDC/OAuth2 configuration.
type AuthConfig struct {
	Issuer       string `json:"issuer"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	AdminGroup   string `json:"admin_group"`
}

// Role represents user authorization level.
type Role string

const (
	RoleViewer Role = "viewer"
	RoleAdmin  Role = "admin"
)

type contextKey string

const roleKey contextKey = "role"

// loadAuthConfig reads OIDC config from environment variables.
func loadAuthConfig() *AuthConfig {
	issuer := envOr("FLEET_OIDC_ISSUER", "")
	if issuer == "" {
		return nil
	}
	return &AuthConfig{
		Issuer:       issuer,
		ClientID:     envOr("FLEET_OIDC_CLIENT_ID", ""),
		ClientSecret: envOr("FLEET_OIDC_CLIENT_SECRET", ""),
		AdminGroup:   envOr("FLEET_OIDC_ADMIN_GROUP", "sigil-admins"),
	}
}

// jwtMiddleware validates JWT tokens on API endpoints.
// If no OIDC is configured, it falls back to API key auth.
func jwtMiddleware(authCfg *AuthConfig, apiKey string, log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health checks
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		// If OIDC is configured, validate JWT
		if authCfg != nil {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, "unauthorized: missing bearer token", http.StatusUnauthorized)
				return
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")
			role, err := validateToken(r.Context(), authCfg, token)
			if err != nil {
				log.Warn("auth: token validation failed", "err", err)
				http.Error(w, "unauthorized: invalid token", http.StatusUnauthorized)
				return
			}

			// Admin-only routes
			if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/policy") {
				if role != RoleAdmin {
					http.Error(w, "forbidden: admin access required", http.StatusForbidden)
					return
				}
			}

			ctx := context.WithValue(r.Context(), roleKey, role)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Fallback: API key auth for ingest
		if r.Method == http.MethodPost && apiKey != "" {
			if r.Header.Get("X-API-Key") != apiKey {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// validateToken performs JWT validation against the OIDC issuer.
// In production, this would fetch the JWKS from the issuer's
// .well-known/openid-configuration and validate the token signature.
// For now, it performs a userinfo endpoint check.
func validateToken(ctx context.Context, cfg *AuthConfig, token string) (Role, error) {
	// Discover userinfo endpoint from OIDC configuration
	userinfoURL := cfg.Issuer + "/userinfo"

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, userinfoURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", http.ErrAbortHandler
	}

	var claims struct {
		Groups []string `json:"groups"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&claims); err != nil {
		return "", err
	}

	for _, g := range claims.Groups {
		if g == cfg.AdminGroup {
			return RoleAdmin, nil
		}
	}
	return RoleViewer, nil
}
