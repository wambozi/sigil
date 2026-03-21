package inference

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"testing"
)

// TestMain builds testdata helper binaries before any tests run, then cleans up.
// This ensures they are compiled for the current GOOS/GOARCH rather than being
// pre-compiled for a single platform.
func TestMain(m *testing.M) {
	helpers := []struct{ src, bin string }{
		{"testdata/fake_llama_server.go", "testdata/fake_llama_server"},
		{"testdata/sigterm_ignore.go", "testdata/sigterm_ignore"},
	}
	for _, h := range helpers {
		cmd := exec.Command("go", "build", "-o", h.bin, h.src)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "testdata build failed (%s): %v\n", h.src, err)
			os.Exit(1)
		}
	}

	code := m.Run()

	for _, h := range helpers {
		os.Remove(h.bin)
	}
	os.Exit(code)
}

// testLogger returns a logger that discards all output. Used across all
// test files in this package.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// containsPathComponent reports whether any slash-delimited segment of path
// equals component.
func containsPathComponent(path, component string) bool {
	for {
		i := len(path)
		for i > 0 && path[i-1] != '/' {
			i--
		}
		seg := path[i:]
		if seg == component {
			return true
		}
		if i == 0 {
			return false
		}
		path = path[:i-1]
	}
}
