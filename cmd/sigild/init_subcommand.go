package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wambozi/sigil/internal/assets"
	"github.com/wambozi/sigil/internal/config"
	"github.com/wambozi/sigil/internal/inference"
)

// runInit implements the "sigild init" subcommand.
// It bootstraps everything a new user needs: shell hook, config file,
// data directory, and (on Linux) the systemd user service.
func runInit() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Println("sigild init: bootstrapping Sigil OS daemon")
	fmt.Println()

	// 1. Shell hook
	if err := installShellHook(home); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] shell hook: %v\n", err)
	}

	// 2. Inference setup
	inferenceToml := setupInference(reader)

	// 3. Fleet setup
	fleetToml := setupFleet(reader)

	// 4. Config file
	if err := installConfigWithInference(inferenceToml, fleetToml); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] config: %v\n", err)
	}

	// 5. Data directory
	if err := installDataDir(home); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] data dir: %v\n", err)
	}

	// 6. Systemd service (Linux only)
	if runtime.GOOS == "linux" {
		if err := installSystemdService(home); err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] systemd: %v\n", err)
		}
	}

	fmt.Println()
	fmt.Println("sigild init: done. Start a new shell or source your rc file to activate the hook.")
	return nil
}

// installShellHook appends the appropriate source line to ~/.zshrc or ~/.bashrc.
func installShellHook(home string) error {
	shell := os.Getenv("SHELL")

	var rcFile, hookFile, sourceLine string
	switch {
	case strings.Contains(shell, "zsh"):
		rcFile = filepath.Join(home, ".zshrc")
		hookFile = "shell-hook.zsh"
		sourceLine = `source "$HOME/.config/sigil/shell-hook.zsh"`
	case strings.Contains(shell, "bash"):
		rcFile = filepath.Join(home, ".bashrc")
		hookFile = "shell-hook.bash"
		sourceLine = `source "$HOME/.config/sigil/shell-hook.bash"`
	default:
		fmt.Println("  [skip] shell hook: unrecognised SHELL, install manually")
		return nil
	}

	// Copy hook file to config dir.
	hookSrc := filepath.Join(home, ".config", "sigil", hookFile)
	if err := copyEmbeddedHook(hookFile, hookSrc); err != nil {
		return fmt.Errorf("copy hook: %w", err)
	}

	// Check if already present in rc file.
	rc, err := os.ReadFile(rcFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", rcFile, err)
	}
	if strings.Contains(string(rc), sourceLine) {
		fmt.Printf("  [ok]   shell hook already in %s\n", rcFile)
		return nil
	}

	// Append source line.
	f, err := os.OpenFile(rcFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", rcFile, err)
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, "\n# Sigil OS shell hook\n%s\n", sourceLine)
	if err != nil {
		return err
	}
	fmt.Printf("  [ok]   shell hook appended to %s\n", rcFile)
	return nil
}

// copyEmbeddedHook writes a shell hook from the embedded asset FS to dst.
func copyEmbeddedHook(name, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}

	data, err := assets.FS.ReadFile("scripts/" + name)
	if err != nil {
		return fmt.Errorf("embedded asset scripts/%s: %w", name, err)
	}
	return os.WriteFile(dst, data, 0o644)
}

// promptYN asks a yes/no question and returns true for yes.
func promptYN(reader *bufio.Reader, question, defaultVal string) bool {
	fmt.Print(question + " ")
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer == "" {
		answer = strings.ToLower(defaultVal)
	}
	return answer == "y" || answer == "yes"
}

// promptString asks for string input with a default.
func promptString(reader *bufio.Reader, question, defaultVal string) string {
	fmt.Print(question + " ")
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return defaultVal
	}
	return answer
}

// setupInference prompts the user about local and cloud inference.
func setupInference(reader *bufio.Reader) string {
	fmt.Println()
	fmt.Println("--- Inference Setup ---")

	var localEnabled, cloudEnabled bool
	var mode, provider, model, apiKey string

	// Local inference
	if promptYN(reader, "Enable local AI inference? (requires ~14GB disk for model) [Y/n]", "y") {
		localEnabled = true

		// Check for llama-server
		if _, err := exec.LookPath("llama-server"); err != nil {
			fmt.Println("  [info] llama-server not found in PATH")
			fmt.Println("         Install llama.cpp or set inference.local.server_bin in config")
		} else {
			fmt.Println("  [ok]   llama-server found in PATH")
		}

		// Offer to download model
		if promptYN(reader, "Download default model (LFM2-24B-A2B, ~14GB)? [y/N]", "n") {
			fmt.Println("  Downloading model...")
			path, err := inference.EnsureModel(context.Background(), inference.DefaultModel, os.Stdout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  [warn] model download failed: %v\n", err)
				fmt.Println("         You can download later with: sigilctl model pull")
			} else {
				fmt.Printf("  [ok]   model cached at %s\n", path)
			}
		} else {
			fmt.Println("  [info] run 'sigilctl model pull' later to download the model")
		}
	}

	// Cloud inference
	if promptYN(reader, "Enable cloud AI fallback? [y/N]", "n") {
		cloudEnabled = true
		provider = promptString(reader, "  Cloud provider (anthropic/openai) [anthropic]:", "anthropic")
		switch provider {
		case "anthropic":
			model = promptString(reader, "  Model [claude-sonnet-4-20250514]:", "claude-sonnet-4-20250514")
		default:
			model = promptString(reader, "  Model [gpt-4o-mini]:", "gpt-4o-mini")
		}
		apiKey = promptString(reader, "  API key (or set SIGIL_CLOUD_API_KEY env var) []:", "")
	}

	// Determine routing mode
	switch {
	case localEnabled && cloudEnabled:
		mode = "localfirst"
	case localEnabled:
		mode = "local"
	case cloudEnabled:
		mode = "remote"
	default:
		mode = "localfirst"
	}

	// Build TOML snippet
	var b strings.Builder
	fmt.Fprintf(&b, "[inference]\nmode = %q\n\n", mode)
	fmt.Fprintf(&b, "[inference.local]\nenabled = %t\n", localEnabled)
	if localEnabled {
		if serverBin, err := exec.LookPath("llama-server"); err == nil {
			fmt.Fprintf(&b, "server_bin = %q\n", serverBin)
		}
		fmt.Fprintf(&b, "ctx_size = 4096\ngpu_layers = 0\n")
	}
	fmt.Fprintf(&b, "\n[inference.cloud]\nenabled = %t\n", cloudEnabled)
	if cloudEnabled {
		fmt.Fprintf(&b, "provider = %q\nmodel = %q\n", provider, model)
		if apiKey != "" {
			fmt.Fprintf(&b, "api_key = %q\n", apiKey)
		}
	}
	return b.String()
}

// setupFleet prompts the user about fleet reporting.
func setupFleet(reader *bufio.Reader) string {
	fmt.Println()
	fmt.Println("--- Team Insights (Fleet Reporting) ---")

	if !promptYN(reader, "Enable team insights (fleet reporting)? [y/N]", "n") {
		return "[fleet]\nenabled = false\n"
	}

	endpoint := promptString(reader, "  Fleet endpoint URL []:", "")
	if endpoint == "" {
		fmt.Println("  [info] no endpoint configured; fleet reporting will be inactive")
		return "[fleet]\nenabled = false\n"
	}

	return fmt.Sprintf("[fleet]\nenabled = true\nendpoint = %q\n", endpoint)
}

// installConfigWithInference creates config.toml with inference and fleet sections.
func installConfigWithInference(inferenceTOML, fleetTOML string) error {
	cfgPath := config.DefaultPath()
	if _, err := os.Stat(cfgPath); err == nil {
		fmt.Printf("  [ok]   config already exists at %s\n", cfgPath)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(cfgPath), err)
	}

	configContent := fmt.Sprintf(`# Sigil daemon configuration
# Generated by sigild init

[daemon]
log_level = "info"

[notifier]
level = 2
digest_time = "09:00"

%s

[retention]
raw_event_days = 90

%s
`, inferenceTOML, fleetTOML)

	if err := os.WriteFile(cfgPath, []byte(configContent), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", cfgPath, err)
	}
	fmt.Printf("  [ok]   config written to %s\n", cfgPath)
	return nil
}

// installDataDir creates the sigild data directory.
func installDataDir(home string) error {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(base, "sigild")

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	fmt.Printf("  [ok]   data directory: %s\n", dir)
	return nil
}

// installSystemdService copies the unit file and enables the service.
func installSystemdService(home string) error {
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o700); err != nil {
		return fmt.Errorf("mkdir systemd user dir: %w", err)
	}
	dst := filepath.Join(unitDir, "sigild.service")

	data, err := assets.FS.ReadFile("deploy/sigild.service")
	if err != nil {
		return fmt.Errorf("embedded asset deploy/sigild.service: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("write service file: %w", err)
	}
	fmt.Printf("  [ok]   service file written to %s\n", dst)

	// Enable and start the service.
	cmds := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "--now", "sigild"},
	}
	for _, args := range cmds {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			fmt.Printf("  [warn] %s: %v — %s\n", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		} else {
			fmt.Printf("  [ok]   %s\n", strings.Join(args, " "))
		}
	}
	return nil
}
