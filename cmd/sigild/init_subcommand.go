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
	"github.com/wambozi/sigil/internal/plugin"
)

// nonInteractive controls whether init skips all prompts and uses safe defaults.
// Set via --non-interactive flag or SIGIL_NON_INTERACTIVE=1.
var nonInteractive bool

// runInit implements the "sigild init" subcommand.
// It bootstraps everything a new user needs: shell hook, config file,
// data directory, and (on Linux) the systemd user service.
//
// With --non-interactive (or SIGIL_NON_INTERACTIVE=1), it skips all prompts,
// installs the shell hook and config with safe defaults, and exits. No network
// access, no package installs, no service registration.
func runInit() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	// Check flag or env for non-interactive mode.
	if os.Getenv("SIGIL_NON_INTERACTIVE") == "1" {
		nonInteractive = true
	}
	for _, arg := range os.Args[2:] {
		if arg == "--non-interactive" {
			nonInteractive = true
		}
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Println("sigild init: bootstrapping Sigil OS daemon")
	fmt.Println()

	// 1. Shell hook
	if err := installShellHook(home); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] shell hook: %v\n", err)
	}

	if nonInteractive {
		return runInitNonInteractive(home)
	}

	// 2. Watch directories
	watchDirs, repoDirs := setupWatchDirs(reader, home)

	// 3. Inference setup
	inferenceToml := setupInference(reader)

	// 4. ML setup
	mlToml := setupML(reader)

	// 5. Plugins setup
	pluginToml := setupPlugins(reader)

	// 6. Fleet setup
	fleetToml := setupFleet(reader)

	// 7. Config file
	if err := installConfigFile(watchDirs, repoDirs, inferenceToml, mlToml, pluginToml, fleetToml); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] config: %v\n", err)
	}

	// 8. Data directory
	if err := installDataDir(home); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] data dir: %v\n", err)
	}

	// 9. System service (platform-specific auto-start)
	switch runtime.GOOS {
	case "linux":
		if err := installSystemdService(home); err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] systemd: %v\n", err)
		}
	case "darwin":
		if err := installLaunchdService(home); err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] launchd: %v\n", err)
		}
	}

	fmt.Println()
	fmt.Println("sigild init: done. Start a new shell or source your rc file to activate the hook.")
	return nil
}

// runInitNonInteractive performs a minimal init with safe defaults:
// shell hook, default config, data directory. No prompts, no network,
// no package installs, no service registration.
func runInitNonInteractive(home string) error {
	fmt.Println("  [info] non-interactive mode — using defaults")

	// Default watch dir: ~/code (or first existing common dir).
	watchDir := filepath.Join(home, "code")
	for _, candidate := range []string{"code", "projects", "src", "workspace", "dev"} {
		p := filepath.Join(home, candidate)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			watchDir = p
			break
		}
	}

	watchDirs := []string{toTildePath(watchDir, home)}
	inferenceToml := "[inference]\nmode = \"localfirst\"\n\n[inference.local]\nenabled = false\n\n[inference.cloud]\nenabled = false\n"
	mlToml := "[ml]\nmode = \"disabled\"\n"
	fleetToml := "[fleet]\nenabled = false\n"

	if err := installConfigFile(watchDirs, nil, inferenceToml, mlToml, "", fleetToml); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] config: %v\n", err)
	}

	if err := installDataDir(home); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] data dir: %v\n", err)
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
	case strings.Contains(shell, "fish"):
		rcFile = filepath.Join(home, ".config", "fish", "config.fish")
		hookFile = "shell-hook.fish"
		sourceLine = `source $HOME/.config/sigil/shell-hook.fish`
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

	// Ensure parent dir exists (no-op for bash/zsh, needed for fish's ~/.config/fish/).
	if err := os.MkdirAll(filepath.Dir(rcFile), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(rcFile), err)
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

// setupWatchDirs prompts for directories to watch and discovers git repos within them.
func setupWatchDirs(reader *bufio.Reader, home string) (watchDirs, repoDirs []string) {
	fmt.Println()
	fmt.Println("--- Watch Directories ---")
	fmt.Println("  Sigil watches directories for file edits and discovers git repos within them.")

	defaultDir := filepath.Join(home, "code")
	if _, err := os.Stat(defaultDir); err != nil {
		// ~/code doesn't exist, try common alternatives
		for _, candidate := range []string{"projects", "src", "workspace", "dev"} {
			p := filepath.Join(home, candidate)
			if _, err := os.Stat(p); err == nil {
				defaultDir = p
				break
			}
		}
	}

	input := promptString(reader,
		fmt.Sprintf("  Directories to watch (comma-separated) [%s]:", toTildePath(defaultDir, home)),
		toTildePath(defaultDir, home))

	for _, raw := range strings.Split(input, ",") {
		dir := strings.TrimSpace(raw)
		if dir == "" {
			continue
		}
		// Expand ~ for validation but store with ~ prefix
		expanded := dir
		if strings.HasPrefix(dir, "~/") {
			expanded = filepath.Join(home, dir[2:])
		}
		if info, err := os.Stat(expanded); err != nil || !info.IsDir() {
			fmt.Printf("  [warn] %s is not a valid directory, skipping\n", dir)
			continue
		}
		watchDirs = append(watchDirs, dir)
	}

	if len(watchDirs) == 0 {
		watchDirs = []string{toTildePath(defaultDir, home)}
		fmt.Printf("  [info] using default: %s\n", watchDirs[0])
	}

	// Discover git repos within watch dirs
	fmt.Println("  Scanning for git repositories...")
	for _, dir := range watchDirs {
		expanded := dir
		if strings.HasPrefix(dir, "~/") {
			expanded = filepath.Join(home, dir[2:])
		}
		repos := discoverRepoDirs(expanded, home)
		repoDirs = append(repoDirs, repos...)
	}

	fmt.Printf("  [ok]   %d watch director%s, %d git repo%s discovered\n",
		len(watchDirs), plural(len(watchDirs)),
		len(repoDirs), plural(len(repoDirs)))

	if len(repoDirs) > 0 && len(repoDirs) <= 10 {
		for _, r := range repoDirs {
			fmt.Printf("         %s\n", r)
		}
	} else if len(repoDirs) > 10 {
		for _, r := range repoDirs[:5] {
			fmt.Printf("         %s\n", r)
		}
		fmt.Printf("         ... and %d more\n", len(repoDirs)-5)
	}

	return watchDirs, repoDirs
}

// discoverRepoDirs walks root up to 3 levels deep looking for directories
// containing a .git subdirectory. Returns paths with ~ prefix.
func discoverRepoDirs(root, home string) []string {
	var repos []string
	maxDepth := strings.Count(root, string(filepath.Separator)) + 3

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		// Limit depth
		if strings.Count(path, string(filepath.Separator)) > maxDepth {
			return filepath.SkipDir
		}
		// Skip noisy directories
		name := d.Name()
		if name == "node_modules" || name == "vendor" || name == ".cache" || name == ".venv" || name == "venv" {
			return filepath.SkipDir
		}
		// Check for .git
		gitDir := filepath.Join(path, ".git")
		if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
			repos = append(repos, toTildePath(path, home))
			return filepath.SkipDir // don't recurse into repos (no nested submodules)
		}
		return nil
	})
	return repos
}

// toTildePath replaces the home directory prefix with ~.
func toTildePath(path, home string) string {
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
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
// setupML prompts the user about ML prediction sidecar.
func setupML(reader *bufio.Reader) string {
	fmt.Println()
	fmt.Println("--- ML Predictions ---")

	if !promptYN(reader, "Enable local ML predictions (stuck detection, suggestion timing)? [Y/n]", "y") {
		return "[ml]\nmode = \"disabled\"\n"
	}

	// Check if sigil-ml is installed.
	if _, err := exec.LookPath("sigil-ml"); err != nil {
		fmt.Println("  [info] sigil-ml not found in PATH")

		// Try to install via Homebrew.
		if _, brewErr := exec.LookPath("brew"); brewErr == nil {
			if promptYN(reader, "  Install sigil-ml via Homebrew? [Y/n]", "y") {
				fmt.Println("  Installing sigil-ml...")
				cmd := exec.Command("brew", "install", "alecfeeman/sigil/sigil-ml")
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					fmt.Printf("  [warn] brew install failed: %v\n", err)
					fmt.Println("         Install manually: brew install alecfeeman/sigil/sigil-ml")
					fmt.Println("         Or: pip install sigil-ml")
				} else {
					fmt.Println("  [ok]   sigil-ml installed")
				}
			}
		} else {
			fmt.Println("         Install manually: pip install sigil-ml")
			fmt.Println("         Or: uv tool install sigil-ml")
		}
	} else {
		fmt.Println("  [ok]   sigil-ml found in PATH")
	}

	var b strings.Builder
	b.WriteString("[ml]\n")
	b.WriteString("mode = \"local\"\n")
	b.WriteString("retrain_every = 10\n\n")
	b.WriteString("[ml.local]\n")
	b.WriteString("enabled = true\n")
	b.WriteString("server_url = \"http://127.0.0.1:7774\"\n")
	b.WriteString("server_bin = \"sigil-ml\"\n\n")
	b.WriteString("[ml.cloud]\n")
	b.WriteString("enabled = false\n")
	return b.String()
}

// setupPlugins offers to install v1 plugins interactively.
func setupPlugins(reader *bufio.Reader) string {
	fmt.Println()
	fmt.Println("--- Plugins ---")

	v1 := plugin.ByVersion("v1")
	if len(v1) == 0 {
		return ""
	}

	method := plugin.DetectInstallMethod()
	var allToml strings.Builder

	for _, entry := range v1 {
		installed := plugin.IsInstalled(entry.Name)
		status := ""
		if installed {
			status = " [already installed]"
		}

		prompt := fmt.Sprintf("  Install %s plugin (%s)?%s [y/N]", entry.Name, entry.Description, status)
		if !promptYN(reader, prompt, "n") {
			continue
		}

		if !installed {
			if err := plugin.Install(entry.Name, method); err != nil {
				fmt.Printf("  [warn] install failed: %v\n", err)
				fmt.Printf("         Install manually: go install %s\n", entry.GoModule)
				continue
			}
		}

		// Run setup (hook installation, credential prompts).
		toml, err := plugin.Setup(entry.Name, reader)
		if err != nil {
			fmt.Printf("  [warn] setup: %v\n", err)
			continue
		}
		allToml.WriteString(toml)
		allToml.WriteString("\n")
		fmt.Printf("  [ok] %s configured\n", entry.Name)
	}

	return allToml.String()
}

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

// installConfigFile creates config.toml with watch dirs, repo dirs, inference, and fleet sections.
func installConfigFile(watchDirs, repoDirs []string, inferenceTOML, mlTOML, pluginTOML, fleetTOML string) error {
	cfgPath := config.DefaultPath()
	if _, err := os.Stat(cfgPath); err == nil {
		fmt.Printf("  [ok]   config already exists at %s\n", cfgPath)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(cfgPath), err)
	}

	var b strings.Builder
	b.WriteString("# Sigil daemon configuration\n# Generated by sigild init\n\n")
	b.WriteString("[daemon]\nlog_level = \"info\"\n")

	if len(watchDirs) > 0 {
		b.WriteString("watch_dirs = [")
		if len(watchDirs) == 1 {
			fmt.Fprintf(&b, "%q", watchDirs[0])
		} else {
			b.WriteString("\n")
			for _, d := range watchDirs {
				fmt.Fprintf(&b, "  %q,\n", d)
			}
		}
		b.WriteString("]\n")
	}

	if len(repoDirs) > 0 {
		b.WriteString("repo_dirs = [\n")
		for _, d := range repoDirs {
			fmt.Fprintf(&b, "  %q,\n", d)
		}
		b.WriteString("]\n")
	}

	b.WriteString("\n[notifier]\nlevel = 2\ndigest_time = \"09:00\"\n\n")
	b.WriteString(inferenceTOML)
	b.WriteString("\n\n")
	b.WriteString(mlTOML)
	b.WriteString("\n\n")
	if pluginTOML != "" {
		b.WriteString(pluginTOML)
		b.WriteString("\n\n")
	}
	b.WriteString("[retention]\nraw_event_days = 90\n\n")
	b.WriteString(fleetTOML)
	b.WriteString("\n")

	if err := os.WriteFile(cfgPath, []byte(b.String()), 0o600); err != nil {
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

// installLaunchdService creates a LaunchAgent plist and loads it on macOS.
func installLaunchdService(home string) error {
	agentDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return fmt.Errorf("mkdir LaunchAgents: %w", err)
	}
	dst := filepath.Join(agentDir, "com.sigil.sigild.plist")

	// Find the installed sigild binary.
	exe, err := exec.LookPath("sigild")
	if err != nil {
		// Fall back to the current binary path.
		exe, _ = os.Executable()
	}

	logDir := filepath.Join(home, ".local", "share", "sigild")
	_ = os.MkdirAll(logDir, 0o700)

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.sigil.sigild</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s/sigild.log</string>
	<key>StandardErrorPath</key>
	<string>%s/sigild.log</string>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`, exe, logDir, logDir)

	if err := os.WriteFile(dst, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	fmt.Printf("  [ok]   launchd plist written to %s\n", dst)

	// Load the agent (unload first to avoid "already loaded" errors).
	_ = exec.Command("launchctl", "unload", dst).Run()
	out, loadErr := exec.Command("launchctl", "load", dst).CombinedOutput()
	if loadErr != nil {
		fmt.Printf("  [warn] launchctl load: %v — %s\n", loadErr, strings.TrimSpace(string(out)))
	} else {
		fmt.Printf("  [ok]   launchctl load %s\n", filepath.Base(dst))
	}
	return nil
}
