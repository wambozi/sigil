package plugin

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// InstallMethod is how a plugin gets installed.
type InstallMethod string

const (
	InstallGo   InstallMethod = "go"
	InstallBrew InstallMethod = "brew"
)

// Install downloads and installs a plugin by name using the preferred method.
// Plugins that ship with sigild (GoModule == "") are already installed via make build.
func Install(name string, method InstallMethod) error {
	entry := Lookup(name)
	if entry == nil {
		return fmt.Errorf("unknown plugin %q — run 'sigilctl plugin list-available' to see options", name)
	}

	// Plugin ships with sigild — no separate install needed.
	if entry.GoModule == "" && entry.BrewFormula == "" {
		if IsInstalled(name) {
			fmt.Printf("  [ok] %s ships with sigild (already in PATH)\n", entry.Binary)
			return nil
		}
		return fmt.Errorf("%s ships with sigild but %q not found in PATH — reinstall sigild with 'make install'", name, entry.Binary)
	}

	switch method {
	case InstallBrew:
		return installBrew(entry)
	default:
		return installGo(entry)
	}
}

// DetectInstallMethod returns the best install method available.
func DetectInstallMethod() InstallMethod {
	// Prefer go install if Go is available.
	if _, err := exec.LookPath("go"); err == nil {
		return InstallGo
	}
	if _, err := exec.LookPath("brew"); err == nil {
		return InstallBrew
	}
	return InstallGo // default, will fail with a clear error
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

func installGo(entry *RegistryEntry) error {
	if entry.GoModule == "" {
		return fmt.Errorf("plugin %q does not have a Go module — try --brew", entry.Name)
	}

	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("'go' not found in PATH — install Go or use --brew")
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
		return fmt.Errorf("plugin %q does not have a Homebrew formula — try go install", entry.Name)
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
