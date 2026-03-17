package network

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/google/uuid"
)

// authListener wraps an inner net.Listener and enforces bearer-token auth
// before any connection is handed back to the caller.
type authListener struct {
	inner net.Listener
	store *CredentialStore
}

// NewAuthListener returns a net.Listener whose Accept() performs the auth
// handshake defined in contracts/auth-wire-protocol.md before returning
// the connection to the caller.
//
// On auth failure the connection is closed and the accept loop retries.
// On success the connection is returned positioned after the auth exchange.
func NewAuthListener(inner net.Listener, store *CredentialStore) net.Listener {
	return &authListener{inner: inner, store: store}
}

// Accept blocks until an authenticated connection is available.
func (l *authListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.inner.Accept()
		if err != nil {
			return nil, err
		}
		wrapped, err := l.authenticate(conn)
		if err != nil {
			conn.Close()
			continue
		}
		return wrapped, nil
	}
}

func (l *authListener) Close() error   { return l.inner.Close() }
func (l *authListener) Addr() net.Addr { return l.inner.Addr() }

// authRequest is the first JSON line the client must send.
type authRequest struct {
	Method  string `json:"method"`
	Payload struct {
		Token string `json:"token"`
	} `json:"payload"`
}

// authResponse is sent back to the client.
type authResponse struct {
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// authenticate reads and validates the auth handshake from conn.
// Returns a net.Conn that replays any bytes buffered past the auth line.
func (l *authListener) authenticate(conn net.Conn) (net.Conn, error) {
	reader := bufio.NewReader(conn)
	enc := json.NewEncoder(conn)

	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read auth line: %w", err)
	}

	var req authRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil || req.Method != "auth" {
		_ = enc.Encode(authResponse{OK: false, Error: "unauthorized"})
		return nil, fmt.Errorf("invalid auth request")
	}

	ok, cred := l.store.Validate(req.Payload.Token)
	if !ok {
		_ = enc.Encode(authResponse{OK: false, Error: "unauthorized"})
		return nil, fmt.Errorf("unauthorized")
	}

	sessionID := uuid.New().String()
	payload, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"cred_id":    cred.ID,
	})
	if err := enc.Encode(authResponse{OK: true, Payload: payload}); err != nil {
		return nil, fmt.Errorf("write auth response: %w", err)
	}

	// If the bufio.Reader buffered bytes past the auth line, replay them.
	buffered := reader.Buffered()
	if buffered > 0 {
		buf := make([]byte, buffered)
		_, _ = reader.Read(buf)
		return &prefixedConn{Conn: conn, r: io.MultiReader(bytes.NewReader(buf), conn)}, nil
	}
	return conn, nil
}

// prefixedConn wraps a net.Conn, replaying bytes from r before reading
// from the underlying connection.
type prefixedConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixedConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// Ensure the deadline methods delegate to the underlying Conn.
func (c *prefixedConn) SetDeadline(t time.Time) error      { return c.Conn.SetDeadline(t) }
func (c *prefixedConn) SetReadDeadline(t time.Time) error  { return c.Conn.SetReadDeadline(t) }
func (c *prefixedConn) SetWriteDeadline(t time.Time) error { return c.Conn.SetWriteDeadline(t) }
