package ml

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testLogger returns a logger that discards all output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// mockMLServer returns a test HTTP server that simulates the sigil-ml sidecar
// and cloud ML API. It responds to /health, /predict/:endpoint, and /train.
func mockMLServer(predictResult map[string]any, trainResult *TrainResult, statusCode int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			if statusCode != 0 && statusCode != http.StatusOK {
				http.Error(w, "unhealthy", statusCode)
				return
			}
			w.WriteHeader(http.StatusOK)

		case len(r.URL.Path) > len("/predict/") && r.URL.Path[:len("/predict/")] == "/predict/":
			if statusCode != 0 && statusCode != http.StatusOK {
				http.Error(w, "prediction failed", statusCode)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(predictResult)

		case r.URL.Path == "/train":
			if statusCode != 0 && statusCode != http.StatusOK {
				http.Error(w, "training failed", statusCode)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if trainResult != nil {
				json.NewEncoder(w).Encode(trainResult)
			} else {
				json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			}

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

// --- LocalBackend tests ---

func TestLocalBackend_Ping_success(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusOK)
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	require.NoError(t, l.Ping(context.Background()))
}

func TestLocalBackend_Ping_nonOK(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusServiceUnavailable)
	defer ts.Close()

	// Build directly — NewLocal would succeed on ping during construction
	// only if the server returns OK; here we want to test the Ping method
	// in isolation against a server that returns non-OK.
	l := &LocalBackend{
		baseURL:   ts.URL,
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: context.Background(),
		healthCfn: func() {},
	}

	err := l.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

func TestLocalBackend_Ping_badURL(t *testing.T) {
	l := &LocalBackend{
		baseURL:   "http://bad url with spaces",
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: context.Background(),
		healthCfn: func() {},
	}
	err := l.Ping(context.Background())
	require.Error(t, err)
}

func TestLocalBackend_Ping_serverDown(t *testing.T) {
	l := &LocalBackend{
		baseURL:   "http://127.0.0.1:1", // nothing listening
		client:    &http.Client{Timeout: 200 * time.Millisecond},
		log:       testLogger(),
		healthCtx: context.Background(),
		healthCfn: func() {},
	}

	err := l.Ping(context.Background())
	require.Error(t, err)
}

func TestLocalBackend_Predict_success(t *testing.T) {
	result := map[string]any{"label": "stuck", "confidence": 0.9}
	ts := mockMLServer(result, nil, http.StatusOK)
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	pred, err := l.Predict(context.Background(), "stuck", map[string]any{"foo": "bar"})
	require.NoError(t, err)
	require.NotNil(t, pred)
	assert.Equal(t, "stuck", pred.Endpoint)
	assert.Equal(t, "local", pred.Routing)
	assert.GreaterOrEqual(t, pred.LatencyMS, int64(0))
	assert.Equal(t, "stuck", pred.Result["label"])
}

func TestLocalBackend_Predict_httpError(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusInternalServerError)
	defer ts.Close()

	l := &LocalBackend{
		baseURL:   ts.URL,
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: context.Background(),
		healthCfn: func() {},
	}

	_, err := l.Predict(context.Background(), "stuck", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}

func TestLocalBackend_Predict_badURL(t *testing.T) {
	l := &LocalBackend{
		baseURL:   "http://bad url with spaces",
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: context.Background(),
		healthCfn: func() {},
	}
	_, err := l.Predict(context.Background(), "stuck", nil)
	require.Error(t, err)
}

func TestLocalBackend_Predict_marshalError(t *testing.T) {
	// Pass a channel in features — json.Marshal cannot serialize it.
	l := &LocalBackend{
		baseURL:   "http://127.0.0.1:1",
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: context.Background(),
		healthCfn: func() {},
	}
	features := map[string]any{"ch": make(chan int)}
	_, err := l.Predict(context.Background(), "stuck", features)
	require.Error(t, err)
}

func TestLocalBackend_Predict_serverDown(t *testing.T) {
	l := &LocalBackend{
		baseURL:   "http://127.0.0.1:1",
		client:    &http.Client{Timeout: 200 * time.Millisecond},
		log:       testLogger(),
		healthCtx: context.Background(),
		healthCfn: func() {},
	}

	_, err := l.Predict(context.Background(), "stuck", nil)
	require.Error(t, err)
}

func TestLocalBackend_Predict_invalidJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not valid json{{"))
	}))
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = l.Predict(context.Background(), "stuck", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestLocalBackend_Train_success(t *testing.T) {
	tr := &TrainResult{Trained: []string{"stuck"}, Samples: 42, DurationMS: 100}
	ts := mockMLServer(nil, tr, http.StatusOK)
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	result, err := l.Train(context.Background(), "/tmp/sigil.db")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 42, result.Samples)
}

func TestLocalBackend_Train_asyncResponseFallback(t *testing.T) {
	// Server returns a non-TrainResult body — Train should return an empty
	// TrainResult rather than an error (training is async).
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Plain string — cannot decode into TrainResult, triggers the fallback.
		w.Write([]byte(`"accepted"`))
	}))
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	result, err := l.Train(context.Background(), "/tmp/sigil.db")
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestLocalBackend_Train_serverDown(t *testing.T) {
	l := &LocalBackend{
		baseURL:   "http://127.0.0.1:1",
		client:    &http.Client{Timeout: 200 * time.Millisecond},
		log:       testLogger(),
		healthCtx: context.Background(),
		healthCfn: func() {},
	}

	_, err := l.Train(context.Background(), "/tmp/sigil.db")
	require.Error(t, err)
}

func TestLocalBackend_Stop_notManaged(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusOK)
	defer ts.Close()

	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ts.URL}, testLogger())
	require.NoError(t, err)

	// Not managed — Stop should be a no-op.
	require.NoError(t, l.Stop())
}

func TestLocalBackend_Stop_nilProcess(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	l := &LocalBackend{
		log:       testLogger(),
		healthCfn: cancel,
		managed:   true,
		proc:      nil, // no process to stop
	}
	require.NoError(t, l.Stop())
}

func TestLocalBackend_Train_badURL(t *testing.T) {
	l := &LocalBackend{
		baseURL:   "http://bad url with spaces",
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: context.Background(),
		healthCfn: func() {},
	}
	_, err := l.Train(context.Background(), "/tmp/sigil.db")
	require.Error(t, err)
}

func TestLocalBackend_NewLocal_serverBinNotFound(t *testing.T) {
	// When no server is running and ServerBin is not in PATH, NewLocal returns
	// an error wrapping the startServer failure.
	cfg := LocalConfig{
		Enabled:   true,
		ServerURL: "http://127.0.0.1:1", // nothing listening
		ServerBin: "sigil-ml-nonexistent-xyz",
	}
	_, err := NewLocal(cfg, testLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start server")
}

func TestLocalBackend_NewLocal_serverAlreadyRunning(t *testing.T) {
	// When Ping succeeds on first try, NewLocal returns immediately without
	// starting the binary or launching the healthMonitor goroutine.
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthServer.Close()

	l, err := NewLocal(LocalConfig{
		Enabled:   true,
		ServerURL: healthServer.URL,
		ServerBin: "sigil-ml-nonexistent-xyz", // not started because server already up
	}, testLogger())
	require.NoError(t, err)
	require.NotNil(t, l)
	assert.False(t, l.managed)
	_ = l.Stop()
}

func TestLocalBackend_NewLocal_noServerNoBin(t *testing.T) {
	// When no server is running and ServerBin is empty, NewLocal returns
	// a backend pointing at the dead server — no error.
	l, err := NewLocal(LocalConfig{
		Enabled:   true,
		ServerURL: "http://127.0.0.1:1", // nothing listening
		ServerBin: "",
	}, testLogger())
	require.NoError(t, err)
	require.NotNil(t, l)
	assert.False(t, l.managed)
}

func TestLocalBackend_defaultURL(t *testing.T) {
	// When ServerURL is empty and no server is running, NewLocal should return
	// a backend (with default URL) and no error.
	l, err := NewLocal(LocalConfig{Enabled: true, ServerURL: ""}, testLogger())
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:7774", l.baseURL)
}

// --- CloudBackend tests ---

func TestCloudBackend_New_missingBaseURL(t *testing.T) {
	_, err := NewCloud(CloudConfig{Enabled: true, BaseURL: ""}, testLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base_url")
}

func TestCloudBackend_Ping_success(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusOK)
	defer ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, BaseURL: ts.URL}, testLogger())
	require.NoError(t, err)

	require.NoError(t, c.Ping(context.Background()))
}

func TestCloudBackend_Ping_nonOK(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusUnauthorized)
	defer ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, BaseURL: ts.URL}, testLogger())
	require.NoError(t, err)

	err = c.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestCloudBackend_Ping_apiKeyHeader(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, BaseURL: ts.URL, APIKey: "secret"}, testLogger())
	require.NoError(t, err)

	require.NoError(t, c.Ping(context.Background()))
	assert.Equal(t, "Bearer secret", gotAuth)
}

func TestCloudBackend_Ping_noAPIKey(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, BaseURL: ts.URL, APIKey: ""}, testLogger())
	require.NoError(t, err)

	require.NoError(t, c.Ping(context.Background()))
	assert.Equal(t, "", gotAuth)
}

func TestCloudBackend_Predict_success(t *testing.T) {
	result := map[string]any{"probability": 0.75}
	ts := mockMLServer(result, nil, http.StatusOK)
	defer ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, BaseURL: ts.URL, APIKey: "k"}, testLogger())
	require.NoError(t, err)

	pred, err := c.Predict(context.Background(), "duration", map[string]any{"x": 1})
	require.NoError(t, err)
	require.NotNil(t, pred)
	assert.Equal(t, "duration", pred.Endpoint)
	assert.Equal(t, "cloud", pred.Routing)
	assert.GreaterOrEqual(t, pred.LatencyMS, int64(0))
	assert.InDelta(t, 0.75, pred.Result["probability"], 0.001)
}

func TestCloudBackend_Predict_apiKeyHeader(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, BaseURL: ts.URL, APIKey: "mykey"}, testLogger())
	require.NoError(t, err)

	_, err = c.Predict(context.Background(), "suggest", nil)
	require.NoError(t, err)
	assert.Equal(t, "Bearer mykey", gotAuth)
}

func TestCloudBackend_Predict_httpError(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusTooManyRequests)
	defer ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, BaseURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = c.Predict(context.Background(), "stuck", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 429")
}

func TestCloudBackend_Predict_invalidJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json{{"))
	}))
	defer ts.Close()

	c, err := NewCloud(CloudConfig{Enabled: true, BaseURL: ts.URL}, testLogger())
	require.NoError(t, err)

	_, err = c.Predict(context.Background(), "stuck", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

func TestCloudBackend_Predict_serverDown(t *testing.T) {
	c, err := NewCloud(CloudConfig{Enabled: true, BaseURL: "http://127.0.0.1:1"}, testLogger())
	require.NoError(t, err)
	c.client = &http.Client{Timeout: 200 * time.Millisecond}

	_, err = c.Predict(context.Background(), "stuck", nil)
	require.Error(t, err)
}

func TestCloudBackend_Ping_badURL(t *testing.T) {
	// A URL with a space causes http.NewRequestWithContext to return an error.
	c := &CloudBackend{
		baseURL: "http://bad url with spaces",
		client:  &http.Client{Timeout: 5 * time.Second},
		log:     testLogger(),
	}
	err := c.Ping(context.Background())
	require.Error(t, err)
}

func TestCloudBackend_Predict_badURL(t *testing.T) {
	c := &CloudBackend{
		baseURL: "http://bad url with spaces",
		client:  &http.Client{Timeout: 5 * time.Second},
		log:     testLogger(),
	}
	_, err := c.Predict(context.Background(), "stuck", nil)
	require.Error(t, err)
}

func TestCloudBackend_Predict_marshalError(t *testing.T) {
	// Pass a channel in features — json.Marshal cannot serialize it.
	c := &CloudBackend{
		baseURL: "http://127.0.0.1:1",
		client:  &http.Client{Timeout: 5 * time.Second},
		log:     testLogger(),
	}
	features := map[string]any{"ch": make(chan int)}
	_, err := c.Predict(context.Background(), "stuck", features)
	require.Error(t, err)
}

func TestCloudBackend_Train_unsupported(t *testing.T) {
	c, err := NewCloud(CloudConfig{Enabled: true, BaseURL: "http://example.com"}, testLogger())
	require.NoError(t, err)

	_, err = c.Train(context.Background(), "/tmp/sigil.db")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

// --- Engine tests ---

func TestEngine_New_disabled(t *testing.T) {
	e, err := New(Config{Mode: RouteDisabled}, testLogger())
	require.NoError(t, err)
	assert.False(t, e.Enabled())
	assert.Equal(t, RouteDisabled, e.mode)
}

func TestEngine_New_emptyModeIsDisabled(t *testing.T) {
	e, err := New(Config{Mode: ""}, testLogger())
	require.NoError(t, err)
	assert.Equal(t, RouteDisabled, e.mode)
}

func TestEngine_New_localBackend(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusOK)
	defer ts.Close()

	e, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)
	require.NotNil(t, e.local)
	assert.Nil(t, e.cloud)
	assert.True(t, e.Enabled())
}

func TestEngine_New_cloudBackend(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusOK)
	defer ts.Close()

	e, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)
	assert.Nil(t, e.local)
	require.NotNil(t, e.cloud)
}

func TestEngine_New_cloudMissingBaseURLWarns(t *testing.T) {
	// Cloud with empty BaseURL should warn but not hard-fail engine creation.
	e, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ""},
	}, testLogger())
	require.NoError(t, err)
	assert.Nil(t, e.cloud) // backend silently omitted
}

func TestEngine_New_localBackendFails_warnContinues(t *testing.T) {
	// When the local backend fails to initialize, New warns and continues
	// without a local backend.
	e, err := New(Config{
		Mode: RouteLocal,
		Local: LocalConfig{
			Enabled:   true,
			ServerURL: "http://127.0.0.1:1", // nothing listening
			ServerBin: "sigil-ml-nonexistent-xyz",
		},
	}, testLogger())
	require.NoError(t, err)
	assert.Nil(t, e.local) // local backend silently omitted after failure
}

func TestEngine_Predict_disabled(t *testing.T) {
	e, err := New(Config{Mode: RouteDisabled}, testLogger())
	require.NoError(t, err)

	_, err = e.Predict(context.Background(), "stuck", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestEngine_Predict_localMode(t *testing.T) {
	result := map[string]any{"label": "stuck", "confidence": 0.8}
	ts := mockMLServer(result, nil, http.StatusOK)
	defer ts.Close()

	e, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	pred, err := e.Predict(context.Background(), "stuck", map[string]any{"k": "v"})
	require.NoError(t, err)
	assert.Equal(t, "local", pred.Routing)
	assert.Equal(t, "stuck", pred.Endpoint)
}

func TestEngine_Predict_remoteMode(t *testing.T) {
	result := map[string]any{"ok": true}
	ts := mockMLServer(result, nil, http.StatusOK)
	defer ts.Close()

	e, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	pred, err := e.Predict(context.Background(), "suggest", nil)
	require.NoError(t, err)
	assert.Equal(t, "cloud", pred.Routing)
}

func TestEngine_Predict_localFirstFallsBackToCloud(t *testing.T) {
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer failServer.Close()

	result := map[string]any{"label": "cloud"}
	cloudServer := mockMLServer(result, nil, http.StatusOK)
	defer cloudServer.Close()

	e, err := New(Config{
		Mode:  RouteLocalFirst,
		Local: LocalConfig{Enabled: true, ServerURL: failServer.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: cloudServer.URL},
	}, testLogger())
	require.NoError(t, err)

	pred, err := e.Predict(context.Background(), "stuck", nil)
	require.NoError(t, err)
	assert.Equal(t, "cloud", pred.Routing)
}

func TestEngine_Predict_remoteFirstFallsBackToLocal(t *testing.T) {
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer failServer.Close()

	result := map[string]any{"label": "local"}
	localServer := mockMLServer(result, nil, http.StatusOK)
	defer localServer.Close()

	e, err := New(Config{
		Mode:  RouteRemoteFirst,
		Local: LocalConfig{Enabled: true, ServerURL: localServer.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: failServer.URL},
	}, testLogger())
	require.NoError(t, err)

	pred, err := e.Predict(context.Background(), "stuck", nil)
	require.NoError(t, err)
	assert.Equal(t, "local", pred.Routing)
}

func TestEngine_Predict_localMode_noLocalBackend(t *testing.T) {
	e := &Engine{mode: RouteLocal, log: testLogger()}

	_, err := e.Predict(context.Background(), "stuck", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local backend not configured")
}

func TestEngine_Predict_remoteMode_noCloudBackend(t *testing.T) {
	e := &Engine{mode: RouteRemote, log: testLogger()}

	_, err := e.Predict(context.Background(), "stuck", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cloud backend not configured")
}

func TestEngine_Predict_remoteFirstNoBothBackends(t *testing.T) {
	e := &Engine{mode: RouteRemoteFirst, log: testLogger()}

	_, err := e.Predict(context.Background(), "stuck", nil)
	require.Error(t, err)
}

func TestEngine_Predict_localFirstNoBothBackends(t *testing.T) {
	e := &Engine{mode: RouteLocalFirst, log: testLogger()}

	_, err := e.Predict(context.Background(), "stuck", nil)
	require.Error(t, err)
}

func TestEngine_Predict_persistsToStore(t *testing.T) {
	result := map[string]any{"confidence": 0.92}
	ts := mockMLServer(result, nil, http.StatusOK)
	defer ts.Close()

	e, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	store := &stubPredictionStore{}
	e.SetStore(store)

	pred, err := e.Predict(context.Background(), "stuck", nil)
	require.NoError(t, err)
	require.NotNil(t, pred)

	require.Len(t, store.calls, 1)
	assert.Equal(t, "stuck", store.calls[0].model)
	assert.InDelta(t, 0.92, store.calls[0].confidence, 0.001)
}

func TestEngine_Predict_persistsProbabilityField(t *testing.T) {
	// Covers the "probability" branch in the confidence extraction.
	result := map[string]any{"probability": 0.77}
	ts := mockMLServer(result, nil, http.StatusOK)
	defer ts.Close()

	e, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	store := &stubPredictionStore{}
	e.SetStore(store)

	_, err = e.Predict(context.Background(), "suggest", nil)
	require.NoError(t, err)
	require.Len(t, store.calls, 1)
	assert.InDelta(t, 0.77, store.calls[0].confidence, 0.001)
}

func TestEngine_Predict_persistsNoConfidenceField(t *testing.T) {
	// No confidence/probability key — confidence should default to 0.
	result := map[string]any{"label": "stuck"}
	ts := mockMLServer(result, nil, http.StatusOK)
	defer ts.Close()

	e, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	store := &stubPredictionStore{}
	e.SetStore(store)

	_, err = e.Predict(context.Background(), "stuck", nil)
	require.NoError(t, err)
	require.Len(t, store.calls, 1)
	assert.Equal(t, 0.0, store.calls[0].confidence)
}

func TestEngine_Predict_storeErrorLogs_doesNotFail(t *testing.T) {
	result := map[string]any{"ok": true}
	ts := mockMLServer(result, nil, http.StatusOK)
	defer ts.Close()

	e, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	store := &stubPredictionStore{err: errors.New("db full")}
	e.SetStore(store)

	// Store error must not propagate — Predict succeeds.
	pred, err := e.Predict(context.Background(), "stuck", nil)
	require.NoError(t, err)
	require.NotNil(t, pred)
}

func TestEngine_Train_disabled(t *testing.T) {
	e, err := New(Config{Mode: RouteDisabled}, testLogger())
	require.NoError(t, err)

	_, err = e.Train(context.Background(), "/tmp/sigil.db")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestEngine_Train_noLocalBackend(t *testing.T) {
	e := &Engine{mode: RouteLocal, log: testLogger()}

	_, err := e.Train(context.Background(), "/tmp/sigil.db")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no local backend")
}

func TestEngine_Train_success(t *testing.T) {
	tr := &TrainResult{Trained: []string{"stuck", "suggest"}, Samples: 100, DurationMS: 200}
	ts := mockMLServer(nil, tr, http.StatusOK)
	defer ts.Close()

	e, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	result, err := e.Train(context.Background(), "/tmp/sigil.db")
	require.NoError(t, err)
	assert.Equal(t, 100, result.Samples)
}

func TestEngine_Ping_disabled(t *testing.T) {
	e, err := New(Config{Mode: RouteDisabled}, testLogger())
	require.NoError(t, err)

	err = e.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestEngine_Ping_localMode_success(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusOK)
	defer ts.Close()

	e, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	require.NoError(t, e.Ping(context.Background()))
}

func TestEngine_Ping_localMode_noBackend(t *testing.T) {
	e := &Engine{mode: RouteLocal, log: testLogger()}

	err := e.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local backend not configured")
}

func TestEngine_Ping_remoteMode_success(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusOK)
	defer ts.Close()

	e, err := New(Config{
		Mode:  RouteRemote,
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	require.NoError(t, e.Ping(context.Background()))
}

func TestEngine_Ping_remoteMode_noBackend(t *testing.T) {
	e := &Engine{mode: RouteRemote, log: testLogger()}

	err := e.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cloud backend not configured")
}

func TestEngine_Ping_localFirst_localSucceeds(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusOK)
	defer ts.Close()

	e, err := New(Config{
		Mode:  RouteLocalFirst,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
		Cloud: CloudConfig{Enabled: true, BaseURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	require.NoError(t, e.Ping(context.Background()))
}

func TestEngine_Ping_localFirst_localFails_cloudSucceeds(t *testing.T) {
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unhealthy", http.StatusServiceUnavailable)
	}))
	defer failServer.Close()

	goodServer := mockMLServer(nil, nil, http.StatusOK)
	defer goodServer.Close()

	e := &Engine{
		mode:  RouteLocalFirst,
		log:   testLogger(),
		local: mustNewLocalDirect(failServer.URL),
		cloud: mustNewCloudDirect(goodServer.URL),
	}

	require.NoError(t, e.Ping(context.Background()))
}

func TestEngine_Ping_localFirst_noneReachable(t *testing.T) {
	e := &Engine{mode: RouteLocalFirst, log: testLogger()}

	err := e.Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no backend reachable")
}

func TestEngine_Ping_remoteFirst_success(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusOK)
	defer ts.Close()

	e := &Engine{
		mode:  RouteRemoteFirst,
		log:   testLogger(),
		cloud: mustNewCloudDirect(ts.URL),
	}

	require.NoError(t, e.Ping(context.Background()))
}

func TestEngine_Close_noLocalBackend(t *testing.T) {
	e := &Engine{mode: RouteLocal, log: testLogger()}
	require.NoError(t, e.Close())
}

func TestEngine_Close_stoppableLocalBackend(t *testing.T) {
	sb := &stubStoppable{}
	e := &Engine{
		mode:  RouteLocal,
		log:   testLogger(),
		local: sb,
	}

	require.NoError(t, e.Close())
	assert.True(t, sb.stopped)
}

func TestEngine_Close_nonStoppableLocalBackend(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusOK)
	defer ts.Close()

	e, err := New(Config{
		Mode:  RouteLocal,
		Local: LocalConfig{Enabled: true, ServerURL: ts.URL},
	}, testLogger())
	require.NoError(t, err)

	require.NoError(t, e.Close())
}

func TestEngine_Enabled(t *testing.T) {
	tests := []struct {
		name    string
		mode    RoutingMode
		enabled bool
	}{
		{"disabled mode", RouteDisabled, false},
		{"local mode", RouteLocal, true},
		{"remote mode", RouteRemote, true},
		{"localfirst mode", RouteLocalFirst, true},
		{"remotefirst mode", RouteRemoteFirst, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Engine{mode: tt.mode}
			assert.Equal(t, tt.enabled, e.Enabled())
		})
	}
}

func TestEngine_SetStore(t *testing.T) {
	e := &Engine{mode: RouteLocal, log: testLogger()}
	assert.Nil(t, e.store)

	store := &stubPredictionStore{}
	e.SetStore(store)
	assert.Equal(t, store, e.store)
}

// --- Config tests ---

func TestConfig_zero(t *testing.T) {
	var cfg Config
	assert.Equal(t, RoutingMode(""), cfg.Mode)
	assert.Equal(t, 0, cfg.RetrainEvery)
	assert.False(t, cfg.Local.Enabled)
	assert.False(t, cfg.Cloud.Enabled)
}

func TestLocalConfig_defaults(t *testing.T) {
	var cfg LocalConfig
	assert.False(t, cfg.Enabled)
	assert.Equal(t, "", cfg.ServerURL)
	assert.Equal(t, "", cfg.ServerBin)
}

func TestCloudConfig_defaults(t *testing.T) {
	var cfg CloudConfig
	assert.False(t, cfg.Enabled)
	assert.Equal(t, "", cfg.BaseURL)
	assert.Equal(t, "", cfg.APIKey)
}

// --- Routing mode constants ---

func TestRoutingModeConstants(t *testing.T) {
	assert.Equal(t, RoutingMode("local"), RouteLocal)
	assert.Equal(t, RoutingMode("localfirst"), RouteLocalFirst)
	assert.Equal(t, RoutingMode("remotefirst"), RouteRemoteFirst)
	assert.Equal(t, RoutingMode("remote"), RouteRemote)
	assert.Equal(t, RoutingMode("disabled"), RouteDisabled)
}

// --- Prediction and TrainResult types ---

func TestPrediction_JSONRoundTrip(t *testing.T) {
	p := &Prediction{
		Endpoint:  "stuck",
		Result:    map[string]any{"label": "stuck"},
		Routing:   "local",
		LatencyMS: 42,
	}
	b, err := json.Marshal(p)
	require.NoError(t, err)

	var got Prediction
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, p.Endpoint, got.Endpoint)
	assert.Equal(t, p.Routing, got.Routing)
	assert.Equal(t, p.LatencyMS, got.LatencyMS)
}

func TestTrainResult_JSONRoundTrip(t *testing.T) {
	tr := &TrainResult{
		Trained:    []string{"stuck", "suggest"},
		Samples:    50,
		DurationMS: 300,
	}
	b, err := json.Marshal(tr)
	require.NoError(t, err)

	var got TrainResult
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, tr.Samples, got.Samples)
	assert.Equal(t, tr.Trained, got.Trained)
}

// --- Test doubles ---

type predictionCall struct {
	model      string
	result     string
	confidence float64
}

type stubPredictionStore struct {
	calls []predictionCall
	err   error
}

func (s *stubPredictionStore) InsertPrediction(_ context.Context, model, result string, confidence float64, _ *time.Time) error {
	s.calls = append(s.calls, predictionCall{model: model, result: result, confidence: confidence})
	return s.err
}

type stubStoppable struct {
	stopped bool
}

func (s *stubStoppable) Predict(_ context.Context, _ string, _ map[string]any) (*Prediction, error) {
	return &Prediction{Endpoint: "test", Routing: "local"}, nil
}

func (s *stubStoppable) Train(_ context.Context, _ string) (*TrainResult, error) {
	return &TrainResult{}, nil
}

func (s *stubStoppable) Ping(_ context.Context) error { return nil }

func (s *stubStoppable) Stop() error {
	s.stopped = true
	return nil
}

// mustNewLocalDirect builds a LocalBackend that points at url without
// attempting to start or ping a subprocess — used in tests that need
// precise control over which backend errors.
func mustNewLocalDirect(url string) *LocalBackend {
	_, cancel := context.WithCancel(context.Background())
	return &LocalBackend{
		baseURL:   url,
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: context.Background(),
		healthCfn: cancel,
	}
}

// mustNewCloudDirect builds a CloudBackend without validation for test use.
func mustNewCloudDirect(url string) *CloudBackend {
	return &CloudBackend{
		baseURL: url,
		client:  &http.Client{Timeout: 5 * time.Second},
		log:     testLogger(),
	}
}

// --- waitForHealth tests ---

func TestLocalBackend_waitForHealth_immediateSuccess(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusOK)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	l := &LocalBackend{
		baseURL:   ts.URL,
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: ctx,
		healthCfn: cancel,
	}

	require.NoError(t, l.waitForHealth())
}

func TestLocalBackend_waitForHealth_eventualSuccess(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	l := &LocalBackend{
		baseURL:   ts.URL,
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: ctx,
		healthCfn: cancel,
	}

	require.NoError(t, l.waitForHealth())
	assert.GreaterOrEqual(t, attempts, 3)
}

// --- killProcess tests ---

func TestLocalBackend_killProcess_nilProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l := &LocalBackend{
		log:       testLogger(),
		healthCtx: ctx,
		healthCfn: cancel,
	}
	require.NoError(t, l.killProcess())
}

func TestLocalBackend_killProcess_liveProcess(t *testing.T) {
	// Start a real long-running process so we have a valid *os.Process to kill.
	// Use "sleep 30" — it exists on Linux and macOS.
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skip("sleep not available:", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	l := &LocalBackend{
		baseURL:   "http://127.0.0.1:1",
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		managed:   true,
		proc:      cmd.Process,
		healthCtx: ctx,
		healthCfn: cancel,
	}

	require.NoError(t, l.killProcess())
	assert.Nil(t, l.proc)
}

func TestLocalBackend_killProcess_sigkillFallback(t *testing.T) {
	// Create a process that ignores SIGTERM so killProcess falls back to SIGKILL
	// after mlShutdownTimeout (5s). This test takes ~5s by design.
	// Use a shell script with SIGTERM trapped.
	tmp := t.TempDir()
	binPath := tmp + "/sigterm-ignorer"
	// trap SIGTERM using bash and continue sleeping — script will not exit on
	// SIGTERM, forcing killProcess to escalate to SIGKILL after the timeout.
	// 'sleep 60 &; wait' keeps the parent alive while ignoring SIGTERM.
	script := "#!/bin/bash\ntrap '' TERM\nsleep 60 &\nwait\n"
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath)
	if err := cmd.Start(); err != nil {
		t.Skip("cannot start sigterm-ignorer:", err)
	}
	// Do NOT call cmd.Wait() in a goroutine here: killProcess owns the Wait
	// call.  A concurrent Wait would race for the waitpid notification and
	// could cause killProcess's done channel to close prematurely (ECHILD).

	// Allow the shell a moment to set up its signal trap before we send SIGTERM.
	time.Sleep(200 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l := &LocalBackend{
		log:       testLogger(),
		managed:   true,
		proc:      cmd.Process,
		healthCtx: ctx,
		healthCfn: cancel,
	}

	require.NoError(t, l.killProcess())
	assert.Nil(t, l.proc)
}

// --- Stop on managed backend ---

func TestLocalBackend_Stop_managedNilProc(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l := &LocalBackend{
		log:       testLogger(),
		managed:   true,
		proc:      nil,
		healthCtx: ctx,
		healthCfn: cancel,
	}
	require.NoError(t, l.Stop())
}

// --- healthMonitor tests ---

func TestLocalBackend_healthMonitor_stopsOnContextCancel(t *testing.T) {
	ts := mockMLServer(nil, nil, http.StatusOK)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	l := &LocalBackend{
		baseURL:   ts.URL,
		client:    &http.Client{Timeout: 5 * time.Second},
		log:       testLogger(),
		healthCtx: ctx,
		healthCfn: cancel,
	}

	done := make(chan struct{})
	go func() {
		l.healthMonitor()
		close(done)
	}()

	// Cancel the context — healthMonitor should exit.
	cancel()

	select {
	case <-done:
		// healthMonitor exited cleanly.
	case <-time.After(2 * time.Second):
		t.Fatal("healthMonitor did not exit after context cancellation")
	}
}

func TestLocalBackend_healthMonitor_unhealthy_thenCanceled(t *testing.T) {
	// Server that always returns unhealthy so the restart branch fires.
	// We give up via context cancellation after the first tick to avoid
	// spawning real subprocesses.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unhealthy", http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	l := &LocalBackend{
		baseURL:   ts.URL,
		client:    &http.Client{Timeout: 200 * time.Millisecond},
		log:       testLogger(),
		managed:   false, // unmanaged — startServer will fail without a bin
		healthCtx: ctx,
		healthCfn: cancel,
		// pre-fill restarts at max so it takes the "giving up" path without
		// attempting startServer.
		restarts: mlMaxRestarts,
	}

	// Use a very short ticker interval so the unhealthy branch fires quickly.
	// We drive healthMonitor in a goroutine and cancel after a short pause.
	done := make(chan struct{})
	go func() {
		// Swap the ticker period: we directly exercise the logic by calling
		// the unexported monitor in a tight loop simulation.  Since we cannot
		// override the ticker constant, we cancel immediately after starting
		// to avoid a 30-second wait.
		l.healthMonitor()
		close(done)
	}()

	// Give it a moment to start, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("healthMonitor did not exit")
	}
}

// --- startServer tests ---

func TestLocalBackend_startServer_binaryNotFound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l := &LocalBackend{
		baseURL: "http://127.0.0.1:7774",
		client:  &http.Client{Timeout: 5 * time.Second},
		log:     testLogger(),
		cfg: LocalConfig{
			Enabled:   true,
			ServerBin: "sigil-ml-nonexistent-binary-xyz",
		},
		healthCtx: ctx,
		healthCfn: cancel,
	}

	err := l.startServer()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in PATH")
}

func TestLocalBackend_startServer_binaryExistsServesHealth(t *testing.T) {
	// Start a real HTTP server that will respond to /health, then create a
	// shell script that immediately exits (it won't actually serve, but since
	// a server is already "up" on the target port, waitForHealth succeeds
	// on the first Ping before the binary is even started).
	//
	// This covers startServer lines: port extraction, exec.Command, cmd.Start,
	// proc/managed assignment, and the waitForHealth success path.
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthServer.Close()

	// Write a tiny shell script so exec.LookPath succeeds.
	tmp := t.TempDir()
	binPath := tmp + "/sigil-ml-test-ok"
	script := "#!/bin/sh\nsleep 5\n" // stays alive long enough
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmp+":"+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l := &LocalBackend{
		// Point at the live health server so waitForHealth succeeds immediately.
		baseURL: healthServer.URL,
		client:  &http.Client{Timeout: 5 * time.Second},
		log:     testLogger(),
		cfg: LocalConfig{
			Enabled:   true,
			ServerBin: "sigil-ml-test-ok",
		},
		healthCtx: ctx,
		healthCfn: cancel,
	}

	err := l.startServer()
	require.NoError(t, err)

	// Clean up the spawned sleep process.
	l.mu.Lock()
	proc := l.proc
	l.mu.Unlock()
	if proc != nil {
		_ = proc.Kill()
	}
}
