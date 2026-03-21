// Command sigil-plugin-vscode monitors VS Code state by reading its
// recently-opened files and workspace state, then pushes events to sigild.
//
// It polls VS Code's state files rather than requiring an extension install.
// This captures: active workspace, recently opened files, active extensions,
// and debug session state.
//
// Install: ships with sigild (make build / make install).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	defaultIngestURL = "http://127.0.0.1:7775/api/v1/ingest"
	pluginName       = "vscode"
)

type PluginEvent struct {
	Plugin      string         `json:"plugin"`
	Kind        string         `json:"kind"`
	Timestamp   time.Time      `json:"timestamp"`
	Correlation map[string]any `json:"correlation,omitempty"`
	Payload     map[string]any `json:"payload"`
}

var (
	ingestURL       string
	pollInterval    time.Duration
	lastWorkspace   string
	lastActiveFiles map[string]time.Time
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "install" {
		installExtensionHook()
		return
	}

	flag.StringVar(&ingestURL, "sigil-ingest-url", defaultIngestURL, "Sigil ingest URL")
	flag.DurationVar(&pollInterval, "poll-interval", 30*time.Second, "Poll interval")
	flag.Parse()

	lastActiveFiles = make(map[string]time.Time)

	fmt.Fprintf(os.Stderr, "sigil-plugin-vscode: polling every %s\n", pollInterval)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	pollAll()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			fmt.Fprintln(os.Stderr, "sigil-plugin-vscode: shutting down")
			return
		case <-ticker.C:
			pollAll()
		}
	}
}

func pollAll() {
	pollRecentWorkspaces()
	pollRecentFiles()
	pollExtensions()
}

// pollRecentWorkspaces reads VS Code's storage.json to find the active workspace.
func pollRecentWorkspaces() {
	storagePath := vscodeStoragePath()
	if storagePath == "" {
		return
	}

	data, err := os.ReadFile(storagePath)
	if err != nil {
		return
	}

	var storage struct {
		OpenedPathsList struct {
			Workspaces3 []string `json:"workspaces3"`
			Entries     []struct {
				FolderURI string `json:"folderUri"`
			} `json:"entries"`
		} `json:"openedPathsList"`
	}
	if err := json.Unmarshal(data, &storage); err != nil {
		return
	}

	// Get the most recent workspace.
	var workspace string
	if len(storage.OpenedPathsList.Entries) > 0 {
		workspace = storage.OpenedPathsList.Entries[0].FolderURI
	} else if len(storage.OpenedPathsList.Workspaces3) > 0 {
		workspace = storage.OpenedPathsList.Workspaces3[0]
	}

	if workspace == "" || workspace == lastWorkspace {
		return
	}

	// Normalize file:// URI to path.
	workspace = strings.TrimPrefix(workspace, "file://")

	lastWorkspace = workspace

	send(PluginEvent{
		Plugin:    pluginName,
		Kind:      "workspace_change",
		Timestamp: time.Now(),
		Correlation: map[string]any{
			"repo_root": workspace,
		},
		Payload: map[string]any{
			"workspace":               workspace,
			"total_recent_workspaces": len(storage.OpenedPathsList.Entries) + len(storage.OpenedPathsList.Workspaces3),
		},
	})
}

// pollRecentFiles reads VS Code's recently opened files.
func pollRecentFiles() {
	recentPath := vscodeRecentPath()
	if recentPath == "" {
		return
	}

	data, err := os.ReadFile(recentPath)
	if err != nil {
		return
	}

	var recent struct {
		Entries []struct {
			FileURI   string `json:"fileUri"`
			FolderURI string `json:"folderUri,omitempty"`
			Label     string `json:"label,omitempty"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &recent); err != nil {
		return
	}

	// Emit new files that weren't in our last snapshot.
	newFiles := make([]string, 0)
	now := time.Now()
	for _, entry := range recent.Entries {
		if entry.FileURI == "" {
			continue
		}
		filePath := strings.TrimPrefix(entry.FileURI, "file://")
		if _, seen := lastActiveFiles[filePath]; !seen {
			newFiles = append(newFiles, filePath)
		}
		lastActiveFiles[filePath] = now
	}

	if len(newFiles) == 0 {
		return
	}

	// Limit to 10 most recent new files.
	if len(newFiles) > 10 {
		newFiles = newFiles[:10]
	}

	send(PluginEvent{
		Plugin:    pluginName,
		Kind:      "recent_files",
		Timestamp: time.Now(),
		Payload: map[string]any{
			"new_files":    newFiles,
			"total_recent": len(recent.Entries),
		},
	})

	// Prune old entries from tracking map.
	cutoff := now.Add(-1 * time.Hour)
	for path, ts := range lastActiveFiles {
		if ts.Before(cutoff) {
			delete(lastActiveFiles, path)
		}
	}
}

// pollExtensions reads installed VS Code extensions.
func pollExtensions() {
	extDir := vscodeExtensionsDir()
	if extDir == "" {
		return
	}

	entries, err := os.ReadDir(extDir)
	if err != nil {
		return
	}

	var extensions []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Filter to interesting extensions (AI, language servers, etc.)
		lower := strings.ToLower(name)
		if isInterestingExtension(lower) {
			extensions = append(extensions, name)
		}
	}

	if len(extensions) == 0 {
		return
	}

	send(PluginEvent{
		Plugin:    pluginName,
		Kind:      "extensions",
		Timestamp: time.Now(),
		Payload: map[string]any{
			"installed":       extensions,
			"total_installed": len(entries),
		},
	})
}

func isInterestingExtension(name string) bool {
	keywords := []string{
		"copilot", "claude", "cody", "tabnine", "codeium", "continue",
		"python", "go", "rust", "java", "typescript",
		"docker", "kubernetes", "terraform",
		"gitlens", "git-graph",
		"debugger", "debug",
		"test", "jest", "pytest",
		"eslint", "prettier", "pylint",
	}
	for _, kw := range keywords {
		if strings.Contains(name, kw) {
			return true
		}
	}
	return false
}

// --- VS Code paths ---

func vscodeStoragePath() string {
	base := vscodeConfigDir()
	if base == "" {
		return ""
	}
	path := filepath.Join(base, "storage.json")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func vscodeRecentPath() string {
	base := vscodeConfigDir()
	if base == "" {
		return ""
	}
	// VS Code stores recent files in different locations by version.
	for _, name := range []string{"recently-opened.json", "storage.json"} {
		path := filepath.Join(base, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func vscodeExtensionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".vscode", "extensions")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func vscodeConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage")
	case "linux":
		return filepath.Join(home, ".config", "Code", "User", "globalStorage")
	default:
		return ""
	}
}

func installExtensionHook() {
	fmt.Println("sigil-plugin-vscode: no extension install needed")
	fmt.Println("  This plugin polls VS Code's state files directly.")
	fmt.Println("  It will capture: active workspace, recently opened files, and installed extensions.")
}

// --- HTTP ---

func send(event PluginEvent) {
	body, err := json.Marshal(event)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(ingestURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return
	}
	resp.Body.Close()
}

// Ensure io is used.
var _ = io.EOF
