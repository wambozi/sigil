// Package socket implements the Unix domain socket server that the Aether Shell
// (Phase 2) and aetherctl will talk to.  The protocol is newline-delimited
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
}

// New creates a Server.  socketPath is the file-system path for the socket
// (e.g. "/run/user/1000/aetherd.sock").
func New(socketPath string, log *slog.Logger) *Server {
	return &Server{
		socketPath: socketPath,
		handlers:   make(map[string]HandlerFunc),
		log:        log,
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

// Start begins listening.  It removes any stale socket file before binding.
// Returns immediately; the accept loop runs in the background until ctx is
// cancelled.
func (s *Server) Start(ctx context.Context) error {
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

	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			_ = enc.Encode(Response{Error: "invalid JSON"})
			continue
		}

		handler, ok := s.handlers[req.Method]
		if !ok {
			_ = enc.Encode(Response{Error: fmt.Sprintf("unknown method: %s", req.Method)})
			continue
		}

		resp := handler(ctx, req)
		if err := enc.Encode(resp); err != nil {
			s.log.Warn("socket: write response", "err", err)
			return
		}
	}
}

// --- Built-in payload helpers -----------------------------------------------

// MarshalPayload is a convenience wrapper that marshals v into a RawMessage.
func MarshalPayload(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
