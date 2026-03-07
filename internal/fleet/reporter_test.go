package fleet

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/wambozi/aether/internal/config"
	"github.com/wambozi/aether/internal/event"
	"github.com/wambozi/aether/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	tmp := t.TempDir()
	s, err := store.Open(tmp + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestReporterPreview(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// Insert some AI interactions
	for i := 0; i < 5; i++ {
		_ = s.InsertAIInteraction(ctx, event.AIInteraction{
			QueryCategory: "code_gen",
			Routing:       "local",
			LatencyMS:     100,
			Timestamp:     time.Now(),
		})
	}
	for i := 0; i < 3; i++ {
		_ = s.InsertAIInteraction(ctx, event.AIInteraction{
			QueryCategory: "debug",
			Routing:       "cloud",
			LatencyMS:     200,
			Timestamp:     time.Now(),
		})
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: "http://localhost:9999",
		Interval: "1h",
	}, log)

	report, err := r.Preview(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if report.NodeID == "" {
		t.Error("expected non-empty node ID")
	}
	if report.AIQueryCounts["code_gen"] != 5 {
		t.Errorf("expected 5 code_gen queries, got %d", report.AIQueryCounts["code_gen"])
	}
	if report.AIQueryCounts["debug"] != 3 {
		t.Errorf("expected 3 debug queries, got %d", report.AIQueryCounts["debug"])
	}
	// 5 local out of 8 total
	expectedRatio := 5.0 / 8.0
	if report.CactusRoutingRatio < expectedRatio-0.01 || report.CactusRoutingRatio > expectedRatio+0.01 {
		t.Errorf("expected routing ratio ~%.2f, got %.2f", expectedRatio, report.CactusRoutingRatio)
	}
}

func TestReporterOptOut(t *testing.T) {
	s := testStore(t)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := New(s, config.FleetConfig{
		Enabled:  true,
		Endpoint: "http://localhost:9999",
	}, log)

	if !r.Enabled() {
		t.Error("expected reporter to be enabled")
	}

	r.OptOut()

	if r.Enabled() {
		t.Error("expected reporter to be disabled after opt-out")
	}
	if r.QueueSize() != 0 {
		t.Error("expected empty queue after opt-out")
	}
}

func TestDetectAdoptionTier(t *testing.T) {
	tests := []struct {
		queries    int
		acceptRate float64
		want       int
	}{
		{0, 0, 0},
		{3, 0.1, 1},
		{10, 0.3, 2},
		{25, 0.6, 3},
	}
	for _, tt := range tests {
		got := detectAdoptionTier(tt.queries, tt.acceptRate)
		if got != tt.want {
			t.Errorf("detectAdoptionTier(%d, %.1f) = %d, want %d",
				tt.queries, tt.acceptRate, got, tt.want)
		}
	}
}

func TestIsBuildCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"go build ./...", true},
		{"go test -v ./...", true},
		{"make all", true},
		{"cargo build", true},
		{"npm run build", true},
		{"ls -la", false},
		{"git commit", false},
	}
	for _, tt := range tests {
		if got := isBuildCommand(tt.cmd); got != tt.want {
			t.Errorf("isBuildCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestGenerateUUID(t *testing.T) {
	id := generateUUID()
	if len(id) != 36 {
		t.Errorf("expected UUID length 36, got %d: %s", len(id), id)
	}
	// Check version nibble
	if id[14] != '4' {
		t.Errorf("expected version 4, got %c", id[14])
	}
}
