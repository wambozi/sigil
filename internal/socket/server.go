// Package socket implements the Unix domain socket server that the Sigil Shell
// (Phase 2) and sigilctl will talk to.  The protocol is newline-delimited
// JSON: one Request per line, one Response per line.
package socket

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// Request is the message a client sends to the daemon.
type Request struct {
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Response is what the daemon sends back.
type Response struct {
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// pushEvent is a server-to-client push message sent on a subscription channel.
type pushEvent struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// HandlerFunc processes a single request and returns a response.
type HandlerFunc func(ctx context.Context, req Request) Response

// Server listens on a Unix socket and dispatches requests to registered
// handlers.  Multiple concurrent clients are supported.
type Server struct {
	socketPath string
	handlers   map[string]HandlerFunc
	log        *slog.Logger

	mu       sync.Mutex
	listener net.Listener

	// subMu guards subscribers, which maps topic names to the set of
	// buffered channels belonging to active subscription connections.
	subMu       sync.RWMutex
	subscribers map[string][]chan json.RawMessage
}

// New creates a Server.  socketPath is the file-system path for the socket
// (e.g. "/run/user/1000/sigild.sock").
func New(socketPath string, log *slog.Logger) *Server {
	return &Server{
		socketPath:  socketPath,
		handlers:    make(map[string]HandlerFunc),
		log:         log,
		subscribers: make(map[string][]chan json.RawMessage),
	}
}

// Handle registers a handler for the given method name.
// Panics if method is empty or already registered.
func (s *Server) Handle(method string, fn HandlerFunc) {
	if _, exists := s.handlers[method]; exists {
		panic(fmt.Sprintf("socket: handler for %q already registered", method))
	}
	s.handlers[method] = fn
}

// Notify fans out payload to all subscribers of topic.  Each send is
// non-blocking: if a subscriber's channel is full the message is dropped for
// that subscriber.  Safe to call from any goroutine.
func (s *Server) Notify(topic string, payload json.RawMessage) {
	s.subMu.RLock()
	chans := s.subscribers[topic]
	s.subMu.RUnlock()

	for _, ch := range chans {
		select {
		case ch <- payload:
		default:
			// Subscriber is not keeping up; drop the message rather than block.
		}
	}
}

// SubscriberCount returns the number of active push subscribers for topic.
func (s *Server) SubscriberCount(topic string) int {
	s.subMu.RLock()
	n := len(s.subscribers[topic])
	s.subMu.RUnlock()
	return n
}

// Start begins listening.  It removes any stale socket file before binding.
// Returns immediately; the accept loop runs in the background until ctx is
// cancelled.
func (s *Server) Start(ctx context.Context) error {
	// Ensure the socket directory exists (important on Windows where
	// the socket lives under %LOCALAPPDATA%\sigil\).
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o700); err != nil {
		return fmt.Errorf("socket: mkdir %s: %w", filepath.Dir(s.socketPath), err)
	}

	// Remove stale socket from a previous daemon run.
	_ = os.Remove(s.socketPath)

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("socket: listen %s: %w", s.socketPath, err)
	}

	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	go s.acceptLoop(ctx, ln)
	return nil
}

// ServeListener starts accepting connections from ln and dispatching them
// using the same logic as Start.  Unlike Start, it does not create its own
// listener — the caller is responsible for binding and any pre-accept
// wrapping (e.g. TLS, auth).  The accept loop runs in a background goroutine;
// ServeListener returns immediately.
func (s *Server) ServeListener(ctx context.Context, ln net.Listener) error {
	go s.acceptLoop(ctx, ln)
	return nil
}

// Stop closes the listener, causing the accept loop to exit.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		s.listener.Close()
	}
}

func (s *Server) acceptLoop(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return // clean shutdown
			default:
				s.log.Error("socket: accept", "err", err)
				return
			}
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	enc := json.NewEncoder(conn)

	// Read the first line to determine connection mode.
	if !scanner.Scan() {
		return
	}

	var first Request
	if err := json.Unmarshal(scanner.Bytes(), &first); err != nil {
		_ = enc.Encode(Response{Error: "invalid JSON"})
		return
	}

	// Subscribe mode: the connection becomes a long-lived push channel.
	if first.Method == "subscribe" {
		s.handleSubscribe(ctx, conn, enc, first)
		return
	}

	// Request/response mode: dispatch the first request, then loop.
	s.dispatch(ctx, enc, first)
	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			_ = enc.Encode(Response{Error: "invalid JSON"})
			continue
		}
		s.dispatch(ctx, enc, req)
	}
}

// dispatch routes a single request to its handler and encodes the response.
func (s *Server) dispatch(ctx context.Context, enc *json.Encoder, req Request) {
	handler, ok := s.handlers[req.Method]
	if !ok {
		_ = enc.Encode(Response{Error: fmt.Sprintf("unknown method: %s", req.Method)})
		return
	}
	resp := handler(ctx, req)
	if err := enc.Encode(resp); err != nil {
		s.log.Warn("socket: write response", "err", err)
	}
}

// handleSubscribe upgrades the connection to a push-only subscription channel.
// It parses the topic from the first request payload, registers a buffered
// channel for that topic, sends the acknowledgement, then loops forwarding
// push events until ctx is cancelled or the channel is closed.
func (s *Server) handleSubscribe(ctx context.Context, conn net.Conn, enc *json.Encoder, req Request) {
	var p struct {
		Topic string `json:"topic"`
	}
	if err := json.Unmarshal(req.Payload, &p); err != nil || p.Topic == "" {
		_ = enc.Encode(Response{Error: "subscribe: payload must be {\"topic\":\"<name>\"}"})
		return
	}

	ch := make(chan json.RawMessage, 32)

	s.subMu.Lock()
	s.subscribers[p.Topic] = append(s.subscribers[p.Topic], ch)
	s.subMu.Unlock()

	// Deregister this channel when the connection exits.
	defer func() {
		s.subMu.Lock()
		chans := s.subscribers[p.Topic]
		filtered := chans[:0]
		for _, c := range chans {
			if c != ch {
				filtered = append(filtered, c)
			}
		}
		s.subscribers[p.Topic] = filtered
		s.subMu.Unlock()
	}()

	// Acknowledge the subscription.
	_ = enc.Encode(Response{
		OK:      true,
		Payload: MarshalPayload(map[string]any{"subscribed": true}),
	})

	for {
		select {
		case <-ctx.Done():
			return
		case payload, ok := <-ch:
			if !ok {
				return
			}
			evt := pushEvent{
				Event:   p.Topic,
				Payload: payload,
			}
			if err := enc.Encode(evt); err != nil {
				s.log.Warn("socket: write push event", "topic", p.Topic, "err", err)
				return
			}
		}
	}
}

// --- Built-in payload helpers -----------------------------------------------

// MarshalPayload is a convenience wrapper that marshals v into a RawMessage.
func MarshalPayload(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
