package socket

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func startTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	// Use a short path to stay within Unix socket path limits (108 chars).
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	log := newTestLogger()
	srv := New(sockPath, log)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		srv.Stop()
	})

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return srv, sockPath
}

// send writes a request to the socket and returns the parsed response.
func send(t *testing.T, sockPath string, req Request) Response {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	var resp Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestServer_registeredHandler(t *testing.T) {
	srv, sockPath := startTestServer(t)

	srv.Handle("ping", func(ctx context.Context, req Request) Response {
		return Response{OK: true, Payload: MarshalPayload("pong")}
	})

	resp := send(t, sockPath, Request{Method: "ping"})
	if !resp.OK {
		t.Errorf("expected OK=true, got error: %s", resp.Error)
	}
}

func TestServer_unknownMethod(t *testing.T) {
	_, sockPath := startTestServer(t)

	resp := send(t, sockPath, Request{Method: "does_not_exist"})
	if resp.OK {
		t.Error("expected OK=false for unknown method")
	}
	if resp.Error == "" {
		t.Error("expected non-empty error for unknown method")
	}
}

func TestServer_invalidJSON(t *testing.T) {
	_, sockPath := startTestServer(t)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.Write([]byte("not json at all\n"))

	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.OK {
		t.Error("expected OK=false for invalid JSON input")
	}
}

func TestServer_handlerReceivesPayload(t *testing.T) {
	srv, sockPath := startTestServer(t)

	type echoPayload struct{ Msg string }

	srv.Handle("echo", func(ctx context.Context, req Request) Response {
		var p echoPayload
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return Response{Error: "bad payload"}
		}
		return Response{OK: true, Payload: MarshalPayload(p)}
	})

	payload, _ := json.Marshal(echoPayload{Msg: "hello sigil"})
	resp := send(t, sockPath, Request{Method: "echo", Payload: payload})

	if !resp.OK {
		t.Fatalf("expected OK, got: %s", resp.Error)
	}
	var got echoPayload
	if err := json.Unmarshal(resp.Payload, &got); err != nil {
		t.Fatalf("unmarshal response payload: %v", err)
	}
	if got.Msg != "hello sigil" {
		t.Errorf("echo: got %q, want %q", got.Msg, "hello sigil")
	}
}

func TestServer_removesStaleSocketOnStart(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "stale.sock")

	// Create a stale file at the socket path.
	if err := os.WriteFile(sockPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	log := newTestLogger()
	srv := New(sockPath, log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); srv.Stop() })

	// Should not fail even though the path already exists.
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start with stale socket: %v", err)
	}
}

func TestServer_panicOnDuplicateHandler(t *testing.T) {
	_, sockPath := startTestServer(t)
	_ = sockPath

	log := newTestLogger()
	srv := New("/tmp/unused.sock", log)
	srv.Handle("dup", func(_ context.Context, _ Request) Response { return Response{} })

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate handler registration")
		}
	}()
	srv.Handle("dup", func(_ context.Context, _ Request) Response { return Response{} })
}

// --- helpers ----------------------------------------------------------------

// newTestLogger returns a discard slog.Logger so tests don't emit noise.
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}
