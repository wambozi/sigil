package inference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

const DefaultModel = "qwen2.5-1.5b-q4_k_m"

// ModelSpec describes a downloadable model.
type ModelSpec struct {
	Name      string // human-readable name
	Filename  string // filename on disk
	URL       string // download URL
	SHA256    string // expected checksum (hex)
	SizeBytes int64  // expected file size
}

// KnownModels lists models that sigild knows how to fetch.
var KnownModels = map[string]ModelSpec{
	"qwen2.5-1.5b-q4_k_m": {
		Name:      "Qwen 2.5 1.5B (Q4_K_M)",
		Filename:  "qwen2.5-1.5b-instruct-q4_k_m.gguf",
		URL:       "https://huggingface.co/Qwen/Qwen2.5-1.5B-Instruct-GGUF/resolve/main/qwen2.5-1.5b-instruct-q4_k_m.gguf",
		SHA256:    "",
		SizeBytes: 0,
	},
	"lfm2-24b-a2b-q4_k_m": {
		Name:      "Liquid LFM2-24B-A2B (Q4_K_M)",
		Filename:  "lfm2-24b-a2b-q4_k_m.gguf",
		URL:       "https://huggingface.co/LiquidAI/LFM2-24B-A2B-GGUF/resolve/main/LFM2-24B-A2B-Q4_K_M.gguf",
		SHA256:    "",
		SizeBytes: 0,
	},
}

// ModelsDir returns the directory for cached models.
// Default: ~/.local/share/sigild/models/ (Linux/macOS)
//
//	%LOCALAPPDATA%\sigil\sigild\models\ (Windows)
func ModelsDir() string {
	if runtime.GOOS == "windows" {
		appdata := os.Getenv("LOCALAPPDATA")
		if appdata == "" {
			appdata = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		return filepath.Join(appdata, "sigil", "sigild", "models")
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".local", "share")
	}
	return filepath.Join(base, "sigild", "models")
}

// ModelPath returns the full path to a model file if it exists, or empty string.
func ModelPath(name string) string {
	spec, ok := KnownModels[name]
	if !ok {
		return ""
	}
	path := filepath.Join(ModelsDir(), spec.Filename)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// CachedModel holds metadata about a locally cached model.
type CachedModel struct {
	Name string
	Path string
	Size int64
}

// ListCachedModels returns the names and paths of locally cached models.
func ListCachedModels() []CachedModel {
	dir := ModelsDir()
	var result []CachedModel
	for name, spec := range KnownModels {
		path := filepath.Join(dir, spec.Filename)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		result = append(result, CachedModel{Name: name, Path: path, Size: info.Size()})
	}
	return result
}

// EnsureModel downloads the model if not present and verifies its checksum.
// progress receives human-readable status updates.
func EnsureModel(ctx context.Context, name string, progress io.Writer) (string, error) {
	spec, ok := KnownModels[name]
	if !ok {
		return "", fmt.Errorf("unknown model: %s", name)
	}

	dir := ModelsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create models dir: %w", err)
	}

	path := filepath.Join(dir, spec.Filename)

	// Check if already downloaded
	if info, err := os.Stat(path); err == nil {
		// Verify checksum if we have one
		if spec.SHA256 != "" {
			if err := verifyChecksum(path, spec.SHA256); err != nil {
				fmt.Fprintf(progress, "Checksum mismatch for %s, re-downloading...\n", spec.Filename)
			} else {
				fmt.Fprintf(progress, "Model %s already cached (%d MB)\n", name, info.Size()/(1024*1024))
				return path, nil
			}
		} else {
			fmt.Fprintf(progress, "Model %s already cached (%d MB)\n", name, info.Size()/(1024*1024))
			return path, nil
		}
	}

	// Download
	fmt.Fprintf(progress, "Downloading %s from %s...\n", spec.Name, spec.URL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	// Write to temp file first, then rename
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	hasher := sha256.New()
	writer := io.MultiWriter(f, hasher)

	var totalBytes int64
	if resp.ContentLength > 0 {
		totalBytes = resp.ContentLength
	}

	// Copy with progress reporting
	buf := make([]byte, 32*1024)
	var written int64
	var lastPct int
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			_, writeErr := writer.Write(buf[:n])
			if writeErr != nil {
				f.Close()
				os.Remove(tmpPath)
				return "", fmt.Errorf("write: %w", writeErr)
			}
			written += int64(n)

			// Report progress every 5%
			if totalBytes > 0 {
				pct := int(written * 100 / totalBytes)
				if pct/5 > lastPct/5 {
					lastPct = pct
					fmt.Fprintf(progress, "  %d%% (%d / %d MB)\n",
						pct, written/(1024*1024), totalBytes/(1024*1024))
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			f.Close()
			os.Remove(tmpPath)
			return "", fmt.Errorf("download read: %w", readErr)
		}
	}
	f.Close()

	// Verify checksum
	gotHash := hex.EncodeToString(hasher.Sum(nil))
	if spec.SHA256 != "" && gotHash != spec.SHA256 {
		os.Remove(tmpPath)
		return "", fmt.Errorf("checksum mismatch: got %s, want %s", gotHash, spec.SHA256)
	}

	// Rename temp to final
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}

	fmt.Fprintf(progress, "Downloaded %s (%d MB)\n", spec.Name, written/(1024*1024))
	return path, nil
}

// verifyChecksum checks a file's SHA256 against expected.
func verifyChecksum(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != expected {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, expected)
	}
	return nil
}
