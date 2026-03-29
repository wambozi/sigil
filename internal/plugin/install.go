package plugin

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// InstallMethod is how a plugin gets installed.
type InstallMethod string

const (
	InstallGo       InstallMethod = "go"
	InstallBrew     InstallMethod = "brew"
	InstallDownload InstallMethod = "download"
)

// GitHubRepo is the repository where release binaries are published.
const GitHubRepo = "sigil-tech/sigil"

// Install downloads and installs a plugin by name.
// It tries to download a pre-built binary from GitHub Releases first,
// then falls back to go install or brew.
func Install(name string, method InstallMethod) error {
	entry := Lookup(name)
	if entry == nil {
		return fmt.Errorf("unknown plugin %q", name)
	}

	if IsInstalled(name) {
		fmt.Printf("  [ok] %s already installed\n", entry.Binary)
		return nil
	}

	// Try downloading pre-built binary from GitHub Releases first.
	if err := installFromRelease(entry); err == nil {
		return nil
	}

	// Fall back to go install / brew.
	switch method {
	case InstallBrew:
		return installBrew(entry)
	default:
		return installGo(entry)
	}
}

// DetectInstallMethod returns the best install method available.
func DetectInstallMethod() InstallMethod {
	return InstallDownload // prefer pre-built binaries
}

// IsInstalled checks if a plugin binary is in PATH.
func IsInstalled(name string) bool {
	entry := Lookup(name)
	if entry == nil {
		return false
	}
	_, err := exec.LookPath(entry.Binary)
	return err == nil
}

// installFromRelease downloads a pre-built binary from the latest GitHub Release.
func installFromRelease(entry *RegistryEntry) error {
	suffix := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	assetName := fmt.Sprintf("%s-%s", entry.Binary, suffix)

	// Determine install directory.
	installDir := defaultInstallDir()
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("create install dir: %w", err)
	}

	// Fetch latest release tag.
	tagURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", GitHubRepo)
	resp, err := http.Get(tagURL)
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	// Parse tag from JSON (minimal parsing to avoid json dependency bloat).
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read release response: %w", err)
	}
	bodyStr := string(body)
	tagIdx := strings.Index(bodyStr, `"tag_name":"`)
	if tagIdx < 0 {
		return fmt.Errorf("no tag_name in release response")
	}
	tagStart := tagIdx + len(`"tag_name":"`)
	tagEnd := strings.Index(bodyStr[tagStart:], `"`)
	if tagEnd < 0 {
		return fmt.Errorf("malformed tag_name")
	}
	tag := bodyStr[tagStart : tagStart+tagEnd]

	// Download the binary.
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", GitHubRepo, tag, assetName)
	fmt.Printf("  Downloading %s from %s...\n", entry.Binary, tag)

	binResp, err := http.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download %s: %w", assetName, err)
	}
	defer binResp.Body.Close()
	if binResp.StatusCode != 200 {
		return fmt.Errorf("download %s: HTTP %d", assetName, binResp.StatusCode)
	}

	destPath := filepath.Join(installDir, entry.Binary)
	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	if _, err := io.Copy(f, binResp.Body); err != nil {
		f.Close()
		os.Remove(destPath)
		return fmt.Errorf("write %s: %w", destPath, err)
	}
	f.Close()

	fmt.Printf("  [ok] %s installed to %s\n", entry.Binary, destPath)
	return nil
}

// defaultInstallDir returns the directory for plugin binaries.
func defaultInstallDir() string {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "sigil", "bin")
		}
		return filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local", "sigil", "bin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}

func installGo(entry *RegistryEntry) error {
	if entry.GoModule == "" {
		return fmt.Errorf("plugin %q has no Go module and no release binary available — reinstall sigil", entry.Name)
	}

	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("'go' not found in PATH — install Go or wait for a release with pre-built binaries")
	}

	fmt.Printf("  Installing %s via go install...\n", entry.Name)
	cmd := exec.Command("go", "install", entry.GoModule)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install %s: %w", entry.GoModule, err)
	}

	fmt.Printf("  [ok] %s installed\n", entry.Binary)
	return nil
}

func installBrew(entry *RegistryEntry) error {
	if entry.BrewFormula == "" {
		return fmt.Errorf("plugin %q does not have a Homebrew formula", entry.Name)
	}

	if _, err := exec.LookPath("brew"); err != nil {
		return fmt.Errorf("'brew' not found in PATH")
	}

	fmt.Printf("  Installing %s via Homebrew...\n", entry.Name)
	cmd := exec.Command("brew", "install", entry.BrewFormula)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("brew install %s: %w", entry.BrewFormula, err)
	}

	fmt.Printf("  [ok] %s installed\n", entry.Binary)
	return nil
}

// Setup runs the plugin's setup routine (hook installation, credential prompts)
// and returns the TOML config snippet to add to config.toml.
func Setup(name string, reader *bufio.Reader) (string, error) {
	entry := Lookup(name)
	if entry == nil {
		return "", fmt.Errorf("unknown plugin %q", name)
	}

	// Run the plugin's install subcommand if it has one.
	if entry.HasSetup {
		if _, err := exec.LookPath(entry.Binary); err == nil {
			fmt.Printf("  Running %s setup...\n", entry.Binary)
			cmd := exec.Command(entry.Binary, "install")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin
			if err := cmd.Run(); err != nil {
				fmt.Printf("  [warn] setup returned error: %v\n", err)
			}
		}
	}

	// Prompt for required env vars.
	envVars := make(map[string]string)
	for _, ev := range entry.EnvVars {
		existing := os.Getenv(ev.Name)
		if existing != "" {
			fmt.Printf("  %s: using existing env var\n", ev.Name)
			envVars[ev.Name] = existing
			continue
		}

		prompt := fmt.Sprintf("  %s (%s)", ev.Name, ev.Description)
		if !ev.Required {
			prompt += " [optional]"
		}
		prompt += ": "
		fmt.Print(prompt)

		val, _ := reader.ReadString('\n')
		val = strings.TrimSpace(val)
		if val != "" {
			envVars[ev.Name] = val
		} else if ev.Required {
			fmt.Printf("  [warn] %s is required — set it in config.toml or as env var later\n", ev.Name)
		}
	}

	// Build TOML config snippet.
	var b strings.Builder
	fmt.Fprintf(&b, "[plugins.%s]\n", entry.Name)
	b.WriteString("enabled = true\n")
	fmt.Fprintf(&b, "binary = %q\n", entry.Binary)

	if len(envVars) > 0 {
		fmt.Fprintf(&b, "\n[plugins.%s.env]\n", entry.Name)
		for k, v := range envVars {
			fmt.Fprintf(&b, "%s = %q\n", k, v)
		}
	}

	return b.String(), nil
}
