// Command fleet-service is the Fleet Aggregation Layer HTTP service.
// It receives anonymized metrics from Fleet Reporters running in aetherd
// instances, stores them in PostgreSQL, and serves aggregation queries
// for the leadership dashboard.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg := loadConfig()

	db, err := openDB(cfg.DBURL)
	if err != nil {
		log.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := migrateDB(db); err != nil {
		log.Error("failed to run migrations", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	h := &handlers{db: db, log: log, apiKey: cfg.APIKey, cloudCostPerQuery: cfg.CloudCostPerQuery}

	mux.HandleFunc("POST /api/v1/reports", h.handleIngestReport)
	mux.HandleFunc("GET /api/v1/metrics", h.handleQueryMetrics)
	mux.HandleFunc("GET /healthz", h.handleHealthz)
	h.registerPolicyRoutes(mux)

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("fleet service starting", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown error", "err", err)
	}
	log.Info("fleet service stopped")
}

type serviceConfig struct {
	DBURL             string
	ListenAddr        string
	APIKey            string
	CloudCostPerQuery float64
}

func loadConfig() serviceConfig {
	cfg := serviceConfig{
		DBURL:             envOr("FLEET_DB_URL", "postgres://localhost:5432/aether_fleet?sslmode=disable"),
		ListenAddr:        envOr("FLEET_LISTEN_ADDR", ":8090"),
		APIKey:            os.Getenv("FLEET_API_KEY"),
		CloudCostPerQuery: 0.01,
	}
	if v := os.Getenv("FLEET_CLOUD_COST_PER_QUERY"); v != "" {
		// Best-effort parse; fall back to default on error.
		var f float64
		if _, err := fmt.Sscanf(v, "%f", &f); err == nil && f > 0 {
			cfg.CloudCostPerQuery = f
		}
	}
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
