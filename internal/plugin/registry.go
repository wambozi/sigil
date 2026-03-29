package plugin

// Registry is the built-in catalog of known Sigil plugins.
// Each entry describes how to install, configure, and set up a plugin.

// RegistryEntry describes a plugin available for installation.
type RegistryEntry struct {
	Name        string       `json:"name"` // short identifier (e.g. "claude", "jira")
	Description string       `json:"description"`
	Version     string       `json:"version"`      // roadmap version: "v1", "v2", "v3", "v4", "v5", "vX"
	Category    string       `json:"category"`     // "ai", "scrum", "scm", "ci", "knowledge", "communication", "observability", "security", "ide"
	Language    string       `json:"language"`     // "go", "shell", "python"
	GoModule    string       `json:"go_module"`    // go install path (primary install method)
	BrewFormula string       `json:"brew_formula"` // homebrew formula (alternative)
	Binary      string       `json:"binary"`       // expected binary name in PATH after install
	HasSetup    bool         `json:"has_setup"`    // plugin binary supports "install" subcommand
	EnvVars     []EnvVarSpec `json:"env_vars"`     // required/optional env vars for configuration
}

// EnvVarSpec describes an environment variable a plugin needs.
type EnvVarSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret"` // mask input during prompts
}

// Registry returns all known plugins.
func Registry() []RegistryEntry {
	return registry
}

// Lookup returns a plugin by name, or nil if not found.
func Lookup(name string) *RegistryEntry {
	for i := range registry {
		if registry[i].Name == name {
			return &registry[i]
		}
	}
	return nil
}

// ByVersion returns all plugins for a given roadmap version.
func ByVersion(version string) []RegistryEntry {
	var out []RegistryEntry
	for _, e := range registry {
		if e.Version == version {
			out = append(out, e)
		}
	}
	return out
}

var registry = []RegistryEntry{
	// Only plugins with source in plugins/ are included.
	// Future plugins will be added here when their repos are published.
	{
		Name:        "claude",
		Description: "Claude Code AI interaction tracking",
		Version:     "v1",
		Category:    "ai",
		Language:    "go",
		Binary:      "sigil-plugin-claude",
		HasSetup:    true,
	},
	{
		Name:        "jira",
		Description: "Jira stories, acceptance criteria, sprint context",
		Version:     "v1",
		Category:    "scrum",
		Language:    "go",
		Binary:      "sigil-plugin-jira",
		EnvVars: []EnvVarSpec{
			{Name: "JIRA_URL", Description: "Jira instance URL (e.g. https://company.atlassian.net)", Required: true},
			{Name: "JIRA_EMAIL", Description: "Jira account email", Required: true},
			{Name: "JIRA_TOKEN", Description: "Jira API token", Required: true, Secret: true},
		},
	},
	{
		Name:        "github",
		Description: "GitHub PRs, CI status, reviews, issues",
		Version:     "v1",
		Category:    "scm",
		Language:    "go",
		Binary:      "sigil-plugin-github",
		EnvVars: []EnvVarSpec{
			{Name: "GITHUB_TOKEN", Description: "GitHub personal access token (or use gh auth)", Required: false, Secret: true},
		},
	},
	{
		Name:        "vscode",
		Description: "VS Code active file, debug sessions, terminal",
		Version:     "v1",
		Category:    "ide",
		Language:    "go",
		Binary:      "sigil-plugin-vscode",
		HasSetup:    true,
	},
	{
		Name:        "jetbrains",
		Description: "JetBrains IDEs — PyCharm, GoLand, IntelliJ, WebStorm, all in one",
		Version:     "v1",
		Category:    "ide",
		Language:    "go",
		Binary:      "sigil-plugin-jetbrains",
	},
}
