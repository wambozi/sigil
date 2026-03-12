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

func TestMarshalPayload(t *testing.T) {
	type sample struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	raw := MarshalPayload(sample{Name: "sigil", Count: 42})
	if raw == nil {
		t.Fatal("MarshalPayload returned nil")
	}

	var got sample
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got.Name != "sigil" || got.Count != 42 {
		t.Errorf("got %+v, want {Name:sigil Count:42}", got)
	}
}

func TestServer_subscribe(t *testing.T) {
	srv, sockPath := startTestServer(t)
	const topic = "test.topic"

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send subscribe request. The payload is a JSON object embedded as
	// json.RawMessage — not a JSON-encoded string.
	subReq := Request{
		Method:  "subscribe",
		Payload: json.RawMessage(`{"topic":"` + topic + `"}`),
	}
	if err := json.NewEncoder(conn).Encode(subReq); err != nil {
		t.Fatalf("encode subscribe request: %v", err)
	}

	scanner := bufio.NewScanner(conn)

	// Read the acknowledgement response.
	if !scanner.Scan() {
		t.Fatalf("expected ack line, got EOF or error: %v", scanner.Err())
	}
	var ack Response
	if err := json.Unmarshal(scanner.Bytes(), &ack); err != nil {
		t.Fatalf("unmarshal ack: %v", err)
	}
	if !ack.OK {
		t.Fatalf("subscribe ack not OK: %s", ack.Error)
	}
	var ackPayload map[string]any
	if err := json.Unmarshal(ack.Payload, &ackPayload); err != nil {
		t.Fatalf("unmarshal ack payload: %v", err)
	}
	if ackPayload["subscribed"] != true {
		t.Errorf("ack payload: got %v, want subscribed=true", ackPayload)
	}

	// Fan out a notification from a separate goroutine. Wait for the
	// subscriber to register before calling Notify.
	notifyPayload := MarshalPayload(map[string]string{"msg": "hello"})
	srv.Notify(topic, notifyPayload)

	// Read the push event.
	if !scanner.Scan() {
		t.Fatalf("expected push event line, got EOF or error: %v", scanner.Err())
	}
	var evt map[string]json.RawMessage
	if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
		t.Fatalf("unmarshal push event: %v", err)
	}

	var gotEvent string
	if err := json.Unmarshal(evt["event"], &gotEvent); err != nil {
		t.Fatalf("unmarshal event name: %v", err)
	}
	if gotEvent != topic {
		t.Errorf("push event name: got %q, want %q", gotEvent, topic)
	}

	var gotPayload map[string]string
	if err := json.Unmarshal(evt["payload"], &gotPayload); err != nil {
		t.Fatalf("unmarshal push event payload: %v", err)
	}
	if gotPayload["msg"] != "hello" {
		t.Errorf("push event payload: got %v, want msg=hello", gotPayload)
	}
}

func TestServer_subscribeInvalidPayload(t *testing.T) {
	_, sockPath := startTestServer(t)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send subscribe with a payload that is not valid JSON.
	if _, err := conn.Write([]byte("{\"method\":\"subscribe\",\"payload\":\"not an object\"}\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.OK {
		t.Error("expected OK=false for invalid subscribe payload")
	}
	if resp.Error == "" {
		t.Error("expected non-empty error for invalid subscribe payload")
	}
}

func TestServer_subscribeEmptyTopic(t *testing.T) {
	_, sockPath := startTestServer(t)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Payload is valid JSON but topic is the empty string.
	subReq := Request{
		Method:  "subscribe",
		Payload: json.RawMessage(`{"topic":""}`),
	}
	if err := json.NewEncoder(conn).Encode(subReq); err != nil {
		t.Fatalf("encode subscribe request: %v", err)
	}

	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.OK {
		t.Error("expected OK=false for empty topic")
	}
	if resp.Error == "" {
		t.Error("expected non-empty error for empty topic")
	}
}

func TestServer_multipleRequestsSameConnection(t *testing.T) {
	srv, sockPath := startTestServer(t)

	srv.Handle("ping", func(_ context.Context, _ Request) Response {
		return Response{OK: true, Payload: MarshalPayload("pong")}
	})
	srv.Handle("version", func(_ context.Context, _ Request) Response {
		return Response{OK: true, Payload: MarshalPayload("v0.1.0")}
	})

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(bufio.NewReader(conn))

	for _, method := range []string{"ping", "version"} {
		if err := enc.Encode(Request{Method: method}); err != nil {
			t.Fatalf("encode %s request: %v", method, err)
		}
		var resp Response
		if err := dec.Decode(&resp); err != nil {
			t.Fatalf("decode %s response: %v", method, err)
		}
		if !resp.OK {
			t.Errorf("%s: expected OK=true, got error: %s", method, resp.Error)
		}
	}
}

func TestServer_Stop(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "stop.sock")

	log := newTestLogger()
	srv := New(sockPath, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Verify the server is reachable before stopping.
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial before Stop: %v", err)
	}
	conn.Close()

	srv.Stop()

	// After Stop, new connections must be refused.
	_, err = net.Dial("unix", sockPath)
	if err == nil {
		t.Error("expected dial to fail after Stop, but it succeeded")
	}
}

func TestServer_notifyNoSubscribers(t *testing.T) {
	srv, _ := startTestServer(t)

	// Calling Notify for a topic with no subscribers must not panic and must
	// return without blocking.
	srv.Notify("no.subscribers", MarshalPayload("payload"))
}

func TestServer_connectionClosedBeforeSend(t *testing.T) {
	_, sockPath := startTestServer(t)

	// Dial and immediately close without writing anything. The server's
	// scanner.Scan() call returns false and handleConn must return cleanly
	// without panicking or logging an error.
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Close with no data written — this drives the !scanner.Scan() early-exit
	// branch in handleConn.
	conn.Close()
}

func TestServer_invalidJSONInLoop(t *testing.T) {
	srv, sockPath := startTestServer(t)

	srv.Handle("ping", func(_ context.Context, _ Request) Response {
		return Response{OK: true, Payload: MarshalPayload("pong")}
	})

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(bufio.NewReader(conn))

	// First request succeeds — enters the dispatch loop.
	if err := enc.Encode(Request{Method: "ping"}); err != nil {
		t.Fatalf("encode first request: %v", err)
	}
	var first Response
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if !first.OK {
		t.Fatalf("first request: expected OK=true, got: %s", first.Error)
	}

	// Second write is invalid JSON — exercises the error branch inside the
	// scanner loop in handleConn.
	if _, err := conn.Write([]byte("not valid json\n")); err != nil {
		t.Fatalf("write bad json: %v", err)
	}
	var errResp Response
	if err := dec.Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.OK {
		t.Error("expected OK=false for invalid JSON in loop")
	}
	if errResp.Error == "" {
		t.Error("expected non-empty error for invalid JSON in loop")
	}
}

func TestServer_subscribeTwoSubscribersThenUnsubscribeOne(t *testing.T) {
	srv, sockPath := startTestServer(t)
	const topic = "multi.sub"

	// dialAndSubscribe opens a connection, sends a subscribe request, reads
	// the ack, and returns the connection and scanner ready to receive events.
	dialAndSubscribe := func(t *testing.T) (net.Conn, *bufio.Scanner) {
		t.Helper()
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		subReq := Request{
			Method:  "subscribe",
			Payload: json.RawMessage(`{"topic":"` + topic + `"}`),
		}
		if err := json.NewEncoder(conn).Encode(subReq); err != nil {
			conn.Close()
			t.Fatalf("encode subscribe: %v", err)
		}
		sc := bufio.NewScanner(conn)
		if !sc.Scan() {
			conn.Close()
			t.Fatalf("expected ack, got EOF")
		}
		var ack Response
		if err := json.Unmarshal(sc.Bytes(), &ack); err != nil || !ack.OK {
			conn.Close()
			t.Fatalf("bad ack: %v / ok=%v", err, ack.OK)
		}
		return conn, sc
	}

	connA, scA := dialAndSubscribe(t)
	defer connA.Close()

	connB, scB := dialAndSubscribe(t)

	// Close B — this triggers the deregistration defer in handleSubscribe,
	// exercising the filtered = append(filtered, c) path where A survives.
	connB.Close()
	// Drain scB to let handleSubscribe notice the closed connection and
	// run the deferred cleanup before we call Notify.
	for scB.Scan() {
	}

	// Notify — only A should receive the event. The filtered slice must
	// not retain B's channel.
	srv.Notify(topic, MarshalPayload(map[string]string{"msg": "only-a"}))

	if !scA.Scan() {
		t.Fatalf("expected push event on connA, got EOF: %v", scA.Err())
	}
	var evt map[string]json.RawMessage
	if err := json.Unmarshal(scA.Bytes(), &evt); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	var eventName string
	if err := json.Unmarshal(evt["event"], &eventName); err != nil {
		t.Fatalf("unmarshal event name: %v", err)
	}
	if eventName != topic {
		t.Errorf("event name: got %q, want %q", eventName, topic)
	}
}

// --- helpers ----------------------------------------------------------------

// newTestLogger returns a discard slog.Logger so tests don't emit noise.
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}
