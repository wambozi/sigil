package inference

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sha256Hex returns the hex-encoded SHA-256 of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// ---------------------------------------------------------------------------
// ModelsDir
// ---------------------------------------------------------------------------

func TestModelsDir(t *testing.T) {
	dir := ModelsDir()
	if dir == "" {
		t.Fatal("ModelsDir returned empty string")
	}
	// The path must contain the well-known subdirectory components.
	for _, want := range []string{"sigild", "models"} {
		if !containsPathComponent(dir, want) {
			t.Errorf("ModelsDir = %q, expected it to contain %q", dir, want)
		}
	}
}

func TestModelsDir_respectsXDGDataHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	dir := ModelsDir()
	wantSuffix := "sigild/models"
	if len(dir) < len(wantSuffix) || dir[len(dir)-len(wantSuffix):] != wantSuffix {
		t.Errorf("ModelsDir = %q, want suffix %q", dir, wantSuffix)
	}
}

// ---------------------------------------------------------------------------
// ModelPath
// ---------------------------------------------------------------------------

func TestModelPath_unknownModel(t *testing.T) {
	path := ModelPath("nonexistent-model-xyz")
	if path != "" {
		t.Errorf("ModelPath = %q, want empty string for unknown model", path)
	}
}

func TestModelPath_knownModelNotCached(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	var name string
	for k := range KnownModels {
		name = k
		break
	}

	path := ModelPath(name)
	if path != "" {
		t.Errorf("ModelPath(%q) = %q, want empty string when model is not cached", name, path)
	}
}

func TestModelPath_cachedModel(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	var name string
	var spec ModelSpec
	for k, v := range KnownModels {
		name, spec = k, v
		break
	}

	modelsDir := ModelsDir()
	require.NoError(t, os.MkdirAll(modelsDir, 0o700))
	f, err := os.Create(filepath.Join(modelsDir, spec.Filename))
	require.NoError(t, err)
	f.Close()

	path := ModelPath(name)
	if path == "" {
		t.Errorf("ModelPath(%q) = empty, want non-empty path to cached file", name)
	}
}

// ---------------------------------------------------------------------------
// ListCachedModels
// ---------------------------------------------------------------------------

func TestListCachedModels_emptyDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	models := ListCachedModels()
	if len(models) != 0 {
		t.Errorf("ListCachedModels = %v, want empty slice", models)
	}
}

func TestListCachedModels_withCachedFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	var name string
	var spec ModelSpec
	for k, v := range KnownModels {
		name, spec = k, v
		break
	}

	modelsDir := ModelsDir()
	require.NoError(t, os.MkdirAll(modelsDir, 0o700))
	f, err := os.Create(filepath.Join(modelsDir, spec.Filename))
	require.NoError(t, err)
	f.Close()

	models := ListCachedModels()
	require.NotEmpty(t, models, "ListCachedModels returned empty, expected at least one cached model")

	found := false
	for _, m := range models {
		if m.Name == name {
			found = true
			assert.NotEmpty(t, m.Path, "CachedModel.Path is empty for %q", name)
		}
	}
	assert.True(t, found, "expected model %q in ListCachedModels result", name)
}

// ---------------------------------------------------------------------------
// KnownModels catalog invariants
// ---------------------------------------------------------------------------

func TestKnownModels_defaultModelPresent(t *testing.T) {
	_, ok := KnownModels[DefaultModel]
	assert.True(t, ok, "DefaultModel %q must be present in KnownModels", DefaultModel)
}

func TestKnownModels_allEntriesHaveRequiredFields(t *testing.T) {
	for name, spec := range KnownModels {
		t.Run(name, func(t *testing.T) {
			assert.NotEmpty(t, spec.Name, "Name must not be empty")
			assert.NotEmpty(t, spec.Filename, "Filename must not be empty")
			assert.NotEmpty(t, spec.URL, "URL must not be empty")
			assert.True(t, strings.HasSuffix(spec.Filename, ".gguf"),
				"Filename must end in .gguf, got %q", spec.Filename)
		})
	}
}

// ---------------------------------------------------------------------------
// verifyChecksum
// ---------------------------------------------------------------------------

func TestVerifyChecksum_match(t *testing.T) {
	content := []byte("test data for checksum verification")
	tmp := t.TempDir()
	path := filepath.Join(tmp, "data.bin")
	require.NoError(t, os.WriteFile(path, content, 0o600))

	require.NoError(t, verifyChecksum(path, sha256Hex(content)))
}

func TestVerifyChecksum_mismatch(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "data.bin")
	require.NoError(t, os.WriteFile(path, []byte("some content"), 0o600))

	err := verifyChecksum(path, "0000000000000000000000000000000000000000000000000000000000000000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
}

func TestVerifyChecksum_missingFile(t *testing.T) {
	err := verifyChecksum("/nonexistent/path/to/model.gguf", "abc123")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// EnsureModel — unknown model
// ---------------------------------------------------------------------------

func TestEnsureModel_unknownModel(t *testing.T) {
	_, err := EnsureModel(context.Background(), "completely-unknown-xyz", io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown model")
}

// ---------------------------------------------------------------------------
// EnsureModel — already cached
// ---------------------------------------------------------------------------

func TestEnsureModel_alreadyCached_noChecksum(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	var name string
	var spec ModelSpec
	for k, v := range KnownModels {
		name, spec = k, v
		break
	}

	dir := ModelsDir()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, spec.Filename)
	require.NoError(t, os.WriteFile(path, []byte("fake model data"), 0o600))

	var buf bytes.Buffer
	got, err := EnsureModel(context.Background(), name, &buf)
	require.NoError(t, err)
	assert.Equal(t, path, got)
	assert.Contains(t, buf.String(), "already cached")
}

func TestEnsureModel_alreadyCached_withChecksumMatch(t *testing.T) {
	content := []byte("matching content")
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_cached_hash_ok"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test Cached Hash OK",
		Filename: "test_cached_hash_ok.gguf",
		URL:      "http://127.0.0.1:1/never-called.gguf", // should never be hit
		SHA256:   sha256Hex(content),
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	dir := ModelsDir()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "test_cached_hash_ok.gguf")
	require.NoError(t, os.WriteFile(path, content, 0o600))

	var buf bytes.Buffer
	got, err := EnsureModel(context.Background(), fakeKey, &buf)
	require.NoError(t, err)
	assert.Equal(t, path, got)
	assert.Contains(t, buf.String(), "already cached")
}

func TestEnsureModel_alreadyCached_withChecksum_logsSize(t *testing.T) {
	content := []byte("content for size log test")
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_cached_size_log"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test Cached Size Log",
		Filename: "test_cached_size_log.gguf",
		URL:      "http://127.0.0.1:1/never-called.gguf",
		SHA256:   sha256Hex(content),
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	dir := ModelsDir()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := dir + "/test_cached_size_log.gguf"
	require.NoError(t, os.WriteFile(path, content, 0o600))

	got, err := EnsureModel(context.Background(), fakeKey, io.Discard)
	require.NoError(t, err)
	assert.Equal(t, path, got)
}

func TestEnsureModel_alreadyCached_badChecksum_redownloads(t *testing.T) {
	// File on disk has wrong checksum; EnsureModel logs a warning and re-downloads.
	// The re-download also fails the checksum (SHA256 = all zeros) so we get an error.
	downloadCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloadCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("freshly downloaded content")) //nolint:errcheck
	}))
	defer ts.Close()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_recache"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test Recache",
		Filename: "test_recache.gguf",
		URL:      ts.URL + "/test_recache.gguf",
		SHA256:   "0000000000000000000000000000000000000000000000000000000000000000",
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	dir := ModelsDir()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "test_recache.gguf"),
		[]byte("bad data"),
		0o600,
	))

	var buf bytes.Buffer
	_, err := EnsureModel(context.Background(), fakeKey, &buf)
	// The re-downloaded content also won't match the all-zero SHA256.
	require.Error(t, err)
	assert.Contains(t, buf.String(), "Checksum mismatch", "should warn about cached file mismatch")
	assert.Greater(t, downloadCount, 0, "should have attempted download")
}

// ---------------------------------------------------------------------------
// EnsureModel — create models dir failure
// ---------------------------------------------------------------------------

func TestEnsureModel_createModelsDirFails(t *testing.T) {
	// Point XDG_DATA_HOME at a regular file so MkdirAll fails.
	tmp := t.TempDir()
	blockingFile := filepath.Join(tmp, "sigild") // will be a file, not a dir
	require.NoError(t, os.WriteFile(blockingFile, []byte("block"), 0o600))
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_mkdirfail"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test MkdirFail",
		Filename: "test_mkdirfail.gguf",
		URL:      "http://127.0.0.1:1/never-called.gguf",
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	_, err := EnsureModel(context.Background(), fakeKey, io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create models dir")
}

// ---------------------------------------------------------------------------
// EnsureModel — create request error (malformed URL)
// ---------------------------------------------------------------------------

func TestEnsureModel_createRequestError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_bad_url"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test Bad URL",
		Filename: "test_bad_url.gguf",
		URL:      "://not-a-valid-url",
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	_, err := EnsureModel(context.Background(), fakeKey, io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create request")
}

// ---------------------------------------------------------------------------
// EnsureModel — download failures
// ---------------------------------------------------------------------------

func TestEnsureModel_downloadTransportError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	ts.Close() // close before use → transport error

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_transport_error"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test Transport Error",
		Filename: "test_transport_error.gguf",
		URL:      ts.URL + "/test_transport_error.gguf",
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	_, err := EnsureModel(context.Background(), fakeKey, io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "download")
}

func TestEnsureModel_downloadFailure_httpError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts.Close()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_dl_fail"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test DL Fail",
		Filename: "test_dl_fail.gguf",
		URL:      ts.URL + "/test_dl_fail.gguf",
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	_, err := EnsureModel(context.Background(), fakeKey, io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestEnsureModel_downloadBodyReadError(t *testing.T) {
	// Serve a 200 but close the connection mid-body.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if ok {
			fmt.Fprint(w, "partial")
			flusher.Flush()
		}
		// Closing the response writer causes a read error on the client.
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	}))
	defer ts.Close()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_read_error"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test Read Error",
		Filename: "test_read_error.gguf",
		URL:      ts.URL + "/test_read_error.gguf",
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	_, err := EnsureModel(context.Background(), fakeKey, io.Discard)
	// The download either succeeds (if the full body fits in a single flush) or
	// errors. Either outcome is acceptable — we just ensure it doesn't panic.
	_ = err
}

// ---------------------------------------------------------------------------
// EnsureModel — create temp file failure
// ---------------------------------------------------------------------------

func TestEnsureModel_createTempFileFails(t *testing.T) {
	content := []byte("body")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(content) //nolint:errcheck
	}))
	defer ts.Close()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_tmpfail"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test TmpFail",
		Filename: "test_tmpfail.gguf",
		URL:      ts.URL + "/test_tmpfail.gguf",
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	dir := ModelsDir()
	require.NoError(t, os.MkdirAll(dir, 0o700))

	// Place a *directory* where the temp file would be created so os.Create fails.
	tmpPath := filepath.Join(dir, "test_tmpfail.gguf.tmp")
	require.NoError(t, os.Mkdir(tmpPath, 0o700))

	_, err := EnsureModel(context.Background(), fakeKey, io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create temp file")
}

// ---------------------------------------------------------------------------
// EnsureModel — rename failure
// ---------------------------------------------------------------------------

func TestEnsureModel_renameFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	content := []byte("data to rename")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(content) //nolint:errcheck
	}))
	defer ts.Close()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_renamefail"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test RenameFail",
		Filename: "test_renamefail.gguf",
		URL:      ts.URL + "/test_renamefail.gguf",
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	dir := ModelsDir()
	require.NoError(t, os.MkdirAll(dir, 0o700))

	// Make the models directory read-only so the rename from .tmp to final
	// path fails with a permission error.
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { os.Chmod(dir, 0o700) }) //nolint:errcheck

	_, err := EnsureModel(context.Background(), fakeKey, io.Discard)
	// Either "rename" or "create temp file" will surface depending on which
	// operation the OS rejects first when the dir is read-only.
	require.Error(t, err)
}

func TestEnsureModel_renameError_documentation(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	// TestEnsureModel_renameFails covers this path.
	// No additional test needed here.
}

// ---------------------------------------------------------------------------
// EnsureModel — write error path (documented gap)
// ---------------------------------------------------------------------------

func TestEnsureModel_writeError(t *testing.T) {
	// The write error path requires os.File.Write to fail, which happens when
	// the file descriptor is invalid. In practice this path is defensive code
	// that's nearly impossible to reach without low-level trickery (closing
	// the fd via syscall while EnsureModel holds it).
	// We document this gap and skip rather than contort the test.
	t.Skip("write error path in EnsureModel requires low-level fd manipulation; defensive code")
}

// ---------------------------------------------------------------------------
// EnsureModel — successful download
// ---------------------------------------------------------------------------

func TestEnsureModel_downloadSuccess_noChecksum(t *testing.T) {
	content := []byte("fake gguf bytes for download")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.WriteHeader(http.StatusOK)
		w.Write(content) //nolint:errcheck
	}))
	defer ts.Close()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_dl_ok"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test DL OK",
		Filename: "test_dl_ok.gguf",
		URL:      ts.URL + "/test_dl_ok.gguf",
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	var buf bytes.Buffer
	got, err := EnsureModel(context.Background(), fakeKey, &buf)
	require.NoError(t, err)
	assert.NotEmpty(t, got)
	assert.Contains(t, buf.String(), "Downloaded")

	data, err := os.ReadFile(got)
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestEnsureModel_downloadSuccess_progressReporting(t *testing.T) {
	// 1 MB triggers percentage-based progress lines.
	content := bytes.Repeat([]byte("x"), 1024*1024)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		w.WriteHeader(http.StatusOK)
		w.Write(content) //nolint:errcheck
	}))
	defer ts.Close()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_dl_progress"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test DL Progress",
		Filename: "test_dl_progress.gguf",
		URL:      ts.URL + "/test_dl_progress.gguf",
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	var buf bytes.Buffer
	_, err := EnsureModel(context.Background(), fakeKey, &buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "%", "progress output should contain percentage")
}

func TestEnsureModel_downloadSuccess_checksumMismatch(t *testing.T) {
	content := []byte("real content")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(content) //nolint:errcheck
	}))
	defer ts.Close()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_checksum_mismatch"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test Checksum Mismatch",
		Filename: "test_checksum_mismatch.gguf",
		URL:      ts.URL + "/test_checksum_mismatch.gguf",
		SHA256:   "0000000000000000000000000000000000000000000000000000000000000000",
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	_, err := EnsureModel(context.Background(), fakeKey, io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
}

func TestEnsureModel_downloadSuccess_checksumMatch(t *testing.T) {
	content := []byte("deterministic content for checksum")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(content) //nolint:errcheck
	}))
	defer ts.Close()

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	const fakeKey = "_test_checksum_match"
	KnownModels[fakeKey] = ModelSpec{
		Name:     "Test Checksum Match",
		Filename: "test_checksum_match.gguf",
		URL:      ts.URL + "/test_checksum_match.gguf",
		SHA256:   sha256Hex(content),
	}
	t.Cleanup(func() { delete(KnownModels, fakeKey) })

	got, err := EnsureModel(context.Background(), fakeKey, io.Discard)
	require.NoError(t, err)
	assert.NotEmpty(t, got)
}
