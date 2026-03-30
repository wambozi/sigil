// Command llm-proxy is a cloud-hosted API gateway that proxies LLM inference
// requests from sigild instances to OpenAI or Anthropic. It authenticates via
// API key, enforces tier-based access (Pro/Team only), implements provider
// failover, meters requests for billing, and rate-limits per tenant.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg := loadConfig()

	mux := http.NewServeMux()
	p := &proxy{
		log:           log,
		apiKey:        cfg.APIKey,
		openaiKey:     cfg.OpenAIKey,
		anthropicKey:  cfg.AnthropicKey,
		rateLimiters:  &sync.Map{},
		ratePerMinute: cfg.RatePerMinute,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}

	mux.HandleFunc("POST /v1/chat/completions", p.handleProxy)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second, // LLM responses can be slow
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Info("llm-proxy starting", "addr", cfg.ListenAddr)
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
	log.Info("llm-proxy stopped")
}

// config holds service configuration loaded from environment variables.
type config struct {
	ListenAddr    string
	APIKey        string // master API key for auth
	OpenAIKey     string
	AnthropicKey  string
	RatePerMinute int
}

func loadConfig() config {
	rpm := 60
	if v := os.Getenv("RATE_PER_MINUTE"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			rpm = n
		}
	}
	return config{
		ListenAddr:    envOr("LISTEN_ADDR", ":8081"),
		APIKey:        os.Getenv("API_KEY"),
		OpenAIKey:     os.Getenv("OPENAI_API_KEY"),
		AnthropicKey:  os.Getenv("ANTHROPIC_API_KEY"),
		RatePerMinute: rpm,
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// proxy holds dependencies for the LLM proxy handler.
type proxy struct {
	log           *slog.Logger
	apiKey        string
	openaiKey     string
	anthropicKey  string
	rateLimiters  *sync.Map
	ratePerMinute int
	client        *http.Client
}

// rateLimiter implements a simple sliding-window rate limiter per tenant.
type rateLimiter struct {
	mu       sync.Mutex
	tokens   int
	max      int
	lastFill time.Time
}

func (rl *rateLimiter) allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastFill)
	// Refill tokens proportionally to elapsed time.
	refill := int(elapsed.Seconds() * float64(rl.max) / 60.0)
	if refill > 0 {
		rl.tokens += refill
		if rl.tokens > rl.max {
			rl.tokens = rl.max
		}
		rl.lastFill = now
	}

	if rl.tokens <= 0 {
		return false
	}
	rl.tokens--
	return true
}

func (p *proxy) getRateLimiter(tenant string) *rateLimiter {
	if v, ok := p.rateLimiters.Load(tenant); ok {
		return v.(*rateLimiter)
	}
	rl := &rateLimiter{
		tokens:   p.ratePerMinute,
		max:      p.ratePerMinute,
		lastFill: time.Now(),
	}
	actual, _ := p.rateLimiters.LoadOrStore(tenant, rl)
	return actual.(*rateLimiter)
}

// handleProxy authenticates, rate-limits, and proxies LLM requests.
func (p *proxy) handleProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Auth: require Bearer token matching API key.
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != p.apiKey {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Tier validation: reject Free tier (indicated via X-Sigil-Tier header).
	tier := r.Header.Get("X-Sigil-Tier")
	if tier == "free" || tier == "Free" {
		http.Error(w, `{"error":"LLM proxy requires Pro or Team tier"}`, http.StatusForbidden)
		return
	}

	// Rate limit per tenant (identified by X-Sigil-Tenant or fall back to remote addr).
	tenant := r.Header.Get("X-Sigil-Tenant")
	if tenant == "" {
		tenant = r.RemoteAddr
	}
	if !p.getRateLimiter(tenant).allow() {
		w.Header().Set("Retry-After", "60")
		http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
		return
	}

	// Read request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Determine provider from model name in request.
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	model, _ := parsed["model"].(string)

	// Try primary provider, failover to secondary.
	var resp *http.Response
	if strings.Contains(model, "claude") || strings.Contains(model, "anthropic") {
		resp, err = p.forwardToAnthropic(r.Context(), body)
		if err != nil {
			p.log.Warn("anthropic failed, trying openai", "err", err)
			resp, err = p.forwardToOpenAI(r.Context(), body)
		}
	} else {
		resp, err = p.forwardToOpenAI(r.Context(), body)
		if err != nil {
			p.log.Warn("openai failed, trying anthropic", "err", err)
			resp, err = p.forwardToAnthropic(r.Context(), body)
		}
	}

	if err != nil {
		p.log.Error("all providers failed", "err", err)
		http.Error(w, `{"error":"all providers failed"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Forward response headers and body.
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)

	// Meter (async -- don't block response).
	latency := time.Since(start)
	go p.meter(tenant, model, resp.StatusCode, latency)
}

func (p *proxy) forwardToOpenAI(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.openai.com/v1/chat/completions",
		strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.openaiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}
	if resp.StatusCode >= 500 {
		resp.Body.Close()
		return nil, fmt.Errorf("openai: server error %d", resp.StatusCode)
	}
	return resp, nil
}

func (p *proxy) forwardToAnthropic(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.anthropic.com/v1/messages",
		strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("anthropic: create request: %w", err)
	}
	req.Header.Set("x-api-key", p.anthropicKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	if resp.StatusCode >= 500 {
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic: server error %d", resp.StatusCode)
	}
	return resp, nil
}

// meter logs request metadata for billing. No prompt content is stored.
func (p *proxy) meter(tenant, model string, status int, latency time.Duration) {
	p.log.Info("request metered",
		"tenant", tenant,
		"model", model,
		"status", status,
		"latency_ms", latency.Milliseconds(),
	)
}
