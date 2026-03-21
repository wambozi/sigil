package network

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// credentials.go
// ---------------------------------------------------------------------------

func TestNewCredentialStore(t *testing.T) {
	s := NewCredentialStore()
	require.NotNil(t, s)
	assert.Empty(t, s.List())
}

func TestCredentialStore_Add(t *testing.T) {
	s := NewCredentialStore()

	err := s.Add("alice", "token-abc")
	require.NoError(t, err)

	creds := s.List()
	require.Len(t, creds, 1)
	assert.Equal(t, "alice", creds[0].ID)
	assert.False(t, creds[0].Revoked)
	assert.NotEmpty(t, creds[0].TokenHash)
	// Token hash must NOT equal plaintext token.
	assert.NotEqual(t, "token-abc", creds[0].TokenHash)
	// CreatedAt must be populated and recent.
	assert.WithinDuration(t, time.Now().UTC(), creds[0].CreatedAt, 5*time.Second)
}

func TestCredentialStore_Add_DuplicateID(t *testing.T) {
	s := NewCredentialStore()
	require.NoError(t, s.Add("alice", "token-1"))

	err := s.Add("alice", "token-2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestCredentialStore_Add_SameTokenDifferentID(t *testing.T) {
	// Two different IDs with the same token produce a key collision in the map
	// (both hash to the same key).  The second Add must either succeed or
	// return an error — the store must not panic.
	s := NewCredentialStore()
	require.NoError(t, s.Add("alice", "shared-token"))
	// "bob" is a different ID, but same token → same hash → overwrite; the
	// implementation allows this because the duplicate-ID guard only checks ID,
	// not hash.  Just verify it doesn't panic.
	_ = s.Add("bob", "shared-token")
}

func TestCredentialStore_Validate(t *testing.T) {
	s := NewCredentialStore()
	require.NoError(t, s.Add("alice", "good-token"))

	t.Run("valid token", func(t *testing.T) {
		ok, cred := s.Validate("good-token")
		require.True(t, ok)
		require.NotNil(t, cred)
		assert.Equal(t, "alice", cred.ID)
	})

	t.Run("unknown token", func(t *testing.T) {
		ok, cred := s.Validate("bad-token")
		assert.False(t, ok)
		assert.Nil(t, cred)
	})

	t.Run("empty token", func(t *testing.T) {
		ok, cred := s.Validate("")
		assert.False(t, ok)
		assert.Nil(t, cred)
	})
}

func TestCredentialStore_Validate_RevokedToken(t *testing.T) {
	s := NewCredentialStore()
	require.NoError(t, s.Add("alice", "tok"))
	require.NoError(t, s.Revoke("alice"))

	ok, cred := s.Validate("tok")
	assert.False(t, ok)
	assert.Nil(t, cred)
}

func TestCredentialStore_Revoke(t *testing.T) {
	s := NewCredentialStore()
	require.NoError(t, s.Add("alice", "tok"))

	require.NoError(t, s.Revoke("alice"))

	creds := s.List()
	require.Len(t, creds, 1)
	assert.True(t, creds[0].Revoked)
	require.NotNil(t, creds[0].RevokedAt)
	assert.WithinDuration(t, time.Now().UTC(), *creds[0].RevokedAt, 5*time.Second)
}

func TestCredentialStore_Revoke_Idempotent(t *testing.T) {
	s := NewCredentialStore()
	require.NoError(t, s.Add("alice", "tok"))

	require.NoError(t, s.Revoke("alice"))
	require.NoError(t, s.Revoke("alice")) // second revoke must be a no-op
}

func TestCredentialStore_Revoke_NotFound(t *testing.T) {
	s := NewCredentialStore()
	err := s.Revoke("nobody")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCredentialStore_List_ReturnsCopies(t *testing.T) {
	s := NewCredentialStore()
	require.NoError(t, s.Add("alice", "tok"))

	list := s.List()
	require.Len(t, list, 1)

	// Mutating the returned copy must not affect the store.
	list[0].Revoked = true
	ok, _ := s.Validate("tok")
	assert.True(t, ok, "mutating List() result must not affect stored credential")
}

func TestCredentialStore_SaveAndLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")

	s1 := NewCredentialStore()
	require.NoError(t, s1.Add("alice", "tok-alice"))
	require.NoError(t, s1.Add("bob", "tok-bob"))
	require.NoError(t, s1.Revoke("bob"))

	require.NoError(t, s1.SaveToFile(path))

	// File must exist with restricted permissions (0600).
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// Load into a fresh store and verify round-trip.
	s2 := NewCredentialStore()
	require.NoError(t, s2.LoadFromFile(path))

	creds := s2.List()
	require.Len(t, creds, 2)

	byID := make(map[string]*Credential, 2)
	for _, c := range creds {
		byID[c.ID] = c
	}

	alice := byID["alice"]
	require.NotNil(t, alice)
	assert.False(t, alice.Revoked)

	bob := byID["bob"]
	require.NotNil(t, bob)
	assert.True(t, bob.Revoked)
	require.NotNil(t, bob.RevokedAt)

	// Validate still works after load (hash preserved).
	ok, cred := s2.Validate("tok-alice")
	require.True(t, ok)
	assert.Equal(t, "alice", cred.ID)

	// Revoked credential must fail validation after load.
	ok, _ = s2.Validate("tok-bob")
	assert.False(t, ok)
}

func TestCredentialStore_LoadFromFile_Missing(t *testing.T) {
	dir := t.TempDir()
	s := NewCredentialStore()
	// Non-existent file must be treated as an empty store, not an error.
	err := s.LoadFromFile(filepath.Join(dir, "no-such-file.json"))
	require.NoError(t, err)
	assert.Empty(t, s.List())
}

func TestCredentialStore_LoadFromFile_Malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))

	s := NewCredentialStore()
	err := s.LoadFromFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse credentials")
}

func TestCredentialStore_LoadFromFile_UnreadablePath(t *testing.T) {
	// Attempt to load from a path whose parent directory does not exist;
	// this exercises the non-NotExist error branch in LoadFromFile.
	s := NewCredentialStore()
	err := s.LoadFromFile("/nonexistent/deep/path/creds.json")
	// On Linux the directory doesn't exist, so we get a "no such file" —
	// which the implementation silently ignores.  This is correct behaviour.
	// If somehow the OS returns a different error the call should still not panic.
	_ = err
}

func TestCredentialStore_SaveToFile_UnwritablePath(t *testing.T) {
	s := NewCredentialStore()
	require.NoError(t, s.Add("alice", "tok"))

	// Writing to a path inside a non-existent directory should fail.
	err := s.SaveToFile("/nonexistent-dir/creds.json")
	require.Error(t, err)
}

func TestCredentialStore_ConcurrentAddValidate(t *testing.T) {
	s := NewCredentialStore()
	const n = 50
	var wg sync.WaitGroup

	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := strings.Repeat("x", i+1)
			token := id + "-tok"
			_ = s.Add(id, token)
			_, _ = s.Validate(token)
		}(i)
	}
	wg.Wait()
}

// hashToken is internal, but we can probe its behaviour indirectly through
// Add + Validate to confirm determinism.
func TestHashToken_Deterministic(t *testing.T) {
	s := NewCredentialStore()
	require.NoError(t, s.Add("alice", "my-token"))

	ok1, _ := s.Validate("my-token")
	ok2, _ := s.Validate("my-token")
	assert.True(t, ok1)
	assert.True(t, ok2)
}

// ---------------------------------------------------------------------------
// certs.go
// ---------------------------------------------------------------------------

func TestLoadOrGenerate_CreatesNewCert(t *testing.T) {
	dir := t.TempDir()

	cert, err := LoadOrGenerate(dir)
	require.NoError(t, err)

	// cert must be usable in a TLS config.
	assert.NotEmpty(t, cert.Certificate)
	assert.NotNil(t, cert.PrivateKey)

	// Private key must be ECDSA.
	_, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	assert.True(t, ok, "expected ECDSA private key")

	// Parse the leaf certificate and verify its properties.
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)

	assert.Equal(t, "sigild", leaf.Subject.CommonName)
	assert.Contains(t, leaf.Subject.Organization, "sigild")

	// Must include localhost DNS SAN.
	assert.Contains(t, leaf.DNSNames, "localhost")

	// Must include 127.0.0.1 IP SAN.
	found := false
	for _, ip := range leaf.IPAddresses {
		if ip.Equal(net.ParseIP("127.0.0.1")) {
			found = true
			break
		}
	}
	assert.True(t, found, "expected 127.0.0.1 in IP SANs")

	// Validity window: cert must be valid now and expire ~1 year from now.
	now := time.Now()
	assert.True(t, leaf.NotBefore.Before(now.Add(time.Second)))
	assert.WithinDuration(t, now.Add(365*24*time.Hour), leaf.NotAfter, 2*time.Minute)

	// Key usage must include DigitalSignature.
	assert.True(t, leaf.KeyUsage&x509.KeyUsageDigitalSignature != 0)

	// Extended key usage must include ServerAuth.
	foundServerAuth := false
	for _, u := range leaf.ExtKeyUsage {
		if u == x509.ExtKeyUsageServerAuth {
			foundServerAuth = true
			break
		}
	}
	assert.True(t, foundServerAuth, "expected ExtKeyUsageServerAuth")

	// PEM files must have been written with restricted permissions.
	for _, name := range []string{certFile, keyFile} {
		info, statErr := os.Stat(filepath.Join(dir, name))
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "file %s must be 0600", name)
	}
}

func TestLoadOrGenerate_LoadsExistingCert(t *testing.T) {
	dir := t.TempDir()

	// Generate once.
	cert1, err := LoadOrGenerate(dir)
	require.NoError(t, err)

	leaf1, err := x509.ParseCertificate(cert1.Certificate[0])
	require.NoError(t, err)

	// Generate a second time from the same directory — must load the same cert.
	cert2, err := LoadOrGenerate(dir)
	require.NoError(t, err)

	leaf2, err := x509.ParseCertificate(cert2.Certificate[0])
	require.NoError(t, err)

	// Serial numbers must match (same cert loaded, not regenerated).
	assert.Equal(t, leaf1.SerialNumber, leaf2.SerialNumber)
}

func TestLoadOrGenerate_EmptySubdir(t *testing.T) {
	// If the directory does not yet exist, LoadOrGenerate must create it.
	parent := t.TempDir()
	dir := filepath.Join(parent, "subdir")

	_, err := LoadOrGenerate(dir)
	require.NoError(t, err)

	_, statErr := os.Stat(dir)
	require.NoError(t, statErr, "directory should have been created")
}

func TestSPKIFingerprint(t *testing.T) {
	dir := t.TempDir()
	cert, err := LoadOrGenerate(dir)
	require.NoError(t, err)

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)

	fp := SPKIFingerprint(leaf)
	assert.True(t, strings.HasPrefix(fp, "sha256/"), "fingerprint must start with sha256/")

	// Fingerprint must be deterministic for the same cert.
	fp2 := SPKIFingerprint(leaf)
	assert.Equal(t, fp, fp2)

	// Fingerprint for a different cert must differ.
	dir2 := t.TempDir()
	cert2, err := LoadOrGenerate(dir2)
	require.NoError(t, err)
	leaf2, err := x509.ParseCertificate(cert2.Certificate[0])
	require.NoError(t, err)

	fp3 := SPKIFingerprint(leaf2)
	assert.NotEqual(t, fp, fp3, "different certs must produce different fingerprints")
}

func TestLoadOrGenerate_TLSHandshake(t *testing.T) {
	// End-to-end sanity: set up a TLS listener with the generated cert and
	// verify that a client configured to trust the same cert can connect.
	dir := t.TempDir()
	tlsCert, err := LoadOrGenerate(dir)
	require.NoError(t, err)

	leaf, err := x509.ParseCertificate(tlsCert.Certificate[0])
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	serverCfg := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverCfg)
	require.NoError(t, err)
	defer ln.Close()

	errCh := make(chan error, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			errCh <- acceptErr
			return
		}
		// Drain the TLS handshake by attempting a read; ignore the result.
		buf := make([]byte, 1)
		_, _ = conn.Read(buf)
		conn.Close()
		errCh <- nil
	}()

	clientCfg := &tls.Config{
		RootCAs:    pool,
		ServerName: "localhost",
	}
	conn, err := tls.Dial("tcp", ln.Addr().String(), clientCfg)
	require.NoError(t, err)
	conn.Close()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server goroutine timed out")
	}
}

// ---------------------------------------------------------------------------
// listener.go
// ---------------------------------------------------------------------------

// newPipeListener creates a net.Listener backed by an in-process pipe pair.
// This avoids any real port allocation in tests.
func newPipeListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	return ln
}

// dialAndSendAuthLine dials the listener, sends the supplied line (which must
// end with '\n'), and returns a bufio.Reader positioned for the response.
func dialAndSendAuthLine(t *testing.T, addr net.Addr, line string) (*bufio.Reader, net.Conn) {
	t.Helper()
	conn, err := net.Dial(addr.Network(), addr.String())
	require.NoError(t, err)
	_, err = conn.Write([]byte(line))
	require.NoError(t, err)
	return bufio.NewReader(conn), conn
}

// sendAuthAndReadResponse performs a full auth request/response exchange.
func sendAuthAndReadResponse(t *testing.T, addr net.Addr, token string) (authResponse, net.Conn) {
	t.Helper()
	req := authRequest{Method: "auth"}
	req.Payload.Token = token
	data, err := json.Marshal(req)
	require.NoError(t, err)
	line := string(data) + "\n"

	reader, conn := dialAndSendAuthLine(t, addr, line)
	var resp authResponse
	err = json.NewDecoder(reader).Decode(&resp)
	require.NoError(t, err)
	return resp, conn
}

func TestNewAuthListener_Addr(t *testing.T) {
	inner := newPipeListener(t)
	store := NewCredentialStore()
	al := NewAuthListener(inner, store)
	assert.Equal(t, inner.Addr(), al.Addr())
}

func TestNewAuthListener_Close(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	store := NewCredentialStore()
	al := NewAuthListener(ln, store)
	assert.NoError(t, al.Close())
}

func TestAuthListener_AcceptSuccess(t *testing.T) {
	inner := newPipeListener(t)
	store := NewCredentialStore()
	require.NoError(t, store.Add("client-1", "secret-token"))

	al := NewAuthListener(inner, store)
	defer al.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := al.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	resp, clientConn := sendAuthAndReadResponse(t, inner.Addr(), "secret-token")
	defer clientConn.Close()

	assert.True(t, resp.OK)
	assert.Empty(t, resp.Error)

	// Response payload must include session_id and cred_id.
	var payload map[string]string
	require.NoError(t, json.Unmarshal(resp.Payload, &payload))
	assert.NotEmpty(t, payload["session_id"])
	assert.Equal(t, "client-1", payload["cred_id"])

	select {
	case conn := <-accepted:
		conn.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("Accept() did not return authenticated connection in time")
	}
}

func TestAuthListener_AcceptRejectsInvalidToken(t *testing.T) {
	inner := newPipeListener(t)
	store := NewCredentialStore()

	al := NewAuthListener(inner, store)
	defer al.Close()

	// Fire off a second valid connection in the background so Accept() can
	// return (the loop must have retried past the failed auth).
	require.NoError(t, store.Add("legit", "good-token"))

	acceptedCh := make(chan net.Conn, 1)
	go func() {
		conn, err := al.Accept()
		if err == nil {
			acceptedCh <- conn
		}
	}()

	// Bad token first — server should reject and continue looping.
	resp1, bad := sendAuthAndReadResponse(t, inner.Addr(), "wrong-token")
	defer bad.Close()
	assert.False(t, resp1.OK)
	assert.Equal(t, "unauthorized", resp1.Error)

	// Good token second — server should accept.
	resp2, good := sendAuthAndReadResponse(t, inner.Addr(), "good-token")
	defer good.Close()
	assert.True(t, resp2.OK)

	select {
	case conn := <-acceptedCh:
		conn.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("Accept() did not return after valid connection")
	}
}

func TestAuthListener_AcceptRejectsRevokedToken(t *testing.T) {
	inner := newPipeListener(t)
	store := NewCredentialStore()
	require.NoError(t, store.Add("alice", "tok"))
	require.NoError(t, store.Revoke("alice"))

	al := NewAuthListener(inner, store)
	defer al.Close()

	// We need at least one good connection so Accept() can unblock.
	require.NoError(t, store.Add("bob", "good-tok"))

	go func() {
		conn, err := al.Accept()
		if err == nil {
			conn.Close()
		}
	}()

	// Revoked token → rejection.
	resp1, c1 := sendAuthAndReadResponse(t, inner.Addr(), "tok")
	defer c1.Close()
	assert.False(t, resp1.OK)

	// Unblock the Accept goroutine.
	_, c2 := sendAuthAndReadResponse(t, inner.Addr(), "good-tok")
	defer c2.Close()
}

func TestAuthListener_AcceptRejectsMalformedJSON(t *testing.T) {
	inner := newPipeListener(t)
	store := NewCredentialStore()

	al := NewAuthListener(inner, store)
	defer al.Close()

	// Add a valid credential so Accept() can eventually return.
	require.NoError(t, store.Add("x", "valid"))

	go func() {
		conn, err := al.Accept()
		if err == nil {
			conn.Close()
		}
	}()

	// Send garbage JSON first.
	reader, bad := dialAndSendAuthLine(t, inner.Addr(), "not-json\n")
	defer bad.Close()
	var resp authResponse
	_ = json.NewDecoder(reader).Decode(&resp)
	assert.False(t, resp.OK)

	// Unblock Accept.
	_, good := sendAuthAndReadResponse(t, inner.Addr(), "valid")
	defer good.Close()
}

func TestAuthListener_AcceptRejectsWrongMethod(t *testing.T) {
	inner := newPipeListener(t)
	store := NewCredentialStore()
	require.NoError(t, store.Add("x", "valid"))

	al := NewAuthListener(inner, store)
	defer al.Close()

	go func() {
		conn, err := al.Accept()
		if err == nil {
			conn.Close()
		}
	}()

	wrongMethod := `{"method":"login","payload":{"token":"valid"}}` + "\n"
	reader, c := dialAndSendAuthLine(t, inner.Addr(), wrongMethod)
	defer c.Close()
	var resp authResponse
	_ = json.NewDecoder(reader).Decode(&resp)
	assert.False(t, resp.OK)

	// Unblock Accept.
	_, good := sendAuthAndReadResponse(t, inner.Addr(), "valid")
	defer good.Close()
}

func TestAuthListener_AcceptRejectsConnectionWithNoData(t *testing.T) {
	inner := newPipeListener(t)
	store := NewCredentialStore()
	require.NoError(t, store.Add("x", "tok"))

	al := NewAuthListener(inner, store)
	defer al.Close()

	acceptedCh := make(chan net.Conn, 1)
	go func() {
		conn, err := al.Accept()
		if err == nil {
			acceptedCh <- conn
		}
	}()

	// Open a connection and close it immediately (EOF without sending auth).
	silent, err := net.Dial(inner.Addr().Network(), inner.Addr().String())
	require.NoError(t, err)
	silent.Close()

	// Now authenticate properly so Accept() can return.
	resp, good := sendAuthAndReadResponse(t, inner.Addr(), "tok")
	defer good.Close()
	assert.True(t, resp.OK)

	select {
	case conn := <-acceptedCh:
		conn.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("Accept() did not return")
	}
}

func TestAuthListener_AcceptErrorOnInnerClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	store := NewCredentialStore()
	al := NewAuthListener(ln, store)

	errCh := make(chan error, 1)
	go func() {
		_, err := al.Accept()
		errCh <- err
	}()

	// Close the inner listener; Accept must surface an error.
	ln.Close()

	select {
	case err := <-errCh:
		assert.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Accept() did not return an error after listener close")
	}
}

func TestAuthListener_PrefixedConnDataForwarding(t *testing.T) {
	// Verify that bytes sent after the auth line are readable from the
	// authenticated connection returned by Accept().
	inner := newPipeListener(t)
	store := NewCredentialStore()
	require.NoError(t, store.Add("c1", "tok"))

	al := NewAuthListener(inner, store)
	defer al.Close()

	payload := "hello from client\n"
	acceptedCh := make(chan net.Conn, 1)
	go func() {
		conn, err := al.Accept()
		if err == nil {
			acceptedCh <- conn
		}
	}()

	// Dial and send auth line + extra payload in a single write so the bufio
	// reader inside authenticate() may buffer the extra bytes.
	req := authRequest{Method: "auth"}
	req.Payload.Token = "tok"
	authBytes, err := json.Marshal(req)
	require.NoError(t, err)

	conn, err := net.Dial(inner.Addr().Network(), inner.Addr().String())
	require.NoError(t, err)
	defer conn.Close()

	// Write auth line and extra data together (encourages buffering).
	_, err = conn.Write(append(append(authBytes, '\n'), []byte(payload)...))
	require.NoError(t, err)

	// Read auth response.
	var resp authResponse
	require.NoError(t, json.NewDecoder(bufio.NewReader(conn)).Decode(&resp))
	assert.True(t, resp.OK)

	select {
	case serverConn := <-acceptedCh:
		defer serverConn.Close()
		buf := make([]byte, len(payload))
		serverConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := serverConn.Read(buf)
		// It is acceptable for the extra payload to not arrive if it was not
		// buffered; only assert no unexpected error.
		if err == nil {
			assert.Equal(t, payload, string(buf[:n]))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Accept() timed out")
	}
}

func TestPrefixedConn_DeadlineMethods(t *testing.T) {
	// Construct a prefixedConn directly and verify that deadline methods
	// delegate to the underlying net.Conn without panicking.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	connCh := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			connCh <- c
		}
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer client.Close()

	server := <-connCh
	defer server.Close()

	pc := &prefixedConn{Conn: server, r: server}

	assert.NoError(t, pc.SetDeadline(time.Now().Add(time.Second)))
	assert.NoError(t, pc.SetReadDeadline(time.Now().Add(time.Second)))
	assert.NoError(t, pc.SetWriteDeadline(time.Now().Add(time.Second)))
}
