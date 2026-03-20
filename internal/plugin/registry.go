package plugin

// Registry is the built-in catalog of known Sigil plugins.
// Each entry describes how to install, configure, and set up a plugin.

// RegistryEntry describes a plugin available for installation.
type RegistryEntry struct {
	Name        string       `json:"name"`         // short identifier (e.g. "claude", "jira")
	Description string       `json:"description"`
	Version     string       `json:"version"`       // roadmap version: "v1", "v2", "v3", "v4", "v5", "vX"
	Category    string       `json:"category"`      // "ai", "scrum", "scm", "ci", "knowledge", "communication", "observability", "security", "ide"
	Language    string       `json:"language"`       // "go", "shell", "python"
	GoModule    string       `json:"go_module"`      // go install path (primary install method)
	BrewFormula string       `json:"brew_formula"`   // homebrew formula (alternative)
	Binary      string       `json:"binary"`         // expected binary name in PATH after install
	HasSetup    bool         `json:"has_setup"`      // plugin binary supports "install" subcommand
	EnvVars     []EnvVarSpec `json:"env_vars"`       // required/optional env vars for configuration
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
	// ── v1: Core Dev Loop ──────────────────────────────────────────────
	{
		Name:        "claude",
		Description: "Claude Code AI interaction tracking",
		Version:     "v1",
		Category:    "ai",
		Language:    "go",
		GoModule:    "", // ships with sigild — built from plugins/sigil-plugin-claude/
		BrewFormula: "", // included in sigil brew formula
		Binary:      "sigil-plugin-claude",
		HasSetup:    true,
	},
	{
		Name:        "jira",
		Description: "Jira stories, acceptance criteria, sprint context",
		Version:     "v1",
		Category:    "scrum",
		Language:    "go",
		GoModule:    "github.com/alecfeeman/sigil-plugin-jira@latest",
		BrewFormula: "alecfeeman/sigil/sigil-plugin-jira",
		Binary:      "sigil-plugin-jira",
		HasSetup:    false,
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
		GoModule:    "", // ships with sigild
		BrewFormula: "",
		Binary:      "sigil-plugin-github",
		HasSetup:    false,
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
		GoModule:    "github.com/alecfeeman/sigil-plugin-vscode@latest",
		BrewFormula: "alecfeeman/sigil/sigil-plugin-vscode",
		Binary:      "sigil-plugin-vscode",
		HasSetup:    true,
	},

	// ── v2: Full Team Workflow ─────────────────────────────────────────
	{
		Name:        "linear",
		Description: "Linear issues, cycles, project status",
		Version:     "v2",
		Category:    "scrum",
		Language:    "go",
		GoModule:    "github.com/alecfeeman/sigil-plugin-linear@latest",
		Binary:      "sigil-plugin-linear",
		EnvVars: []EnvVarSpec{
			{Name: "LINEAR_TOKEN", Description: "Linear API key", Required: true, Secret: true},
		},
	},
	{
		Name:        "confluence",
		Description: "Confluence pages linked to stories",
		Version:     "v2",
		Category:    "knowledge",
		Language:    "go",
		GoModule:    "github.com/alecfeeman/sigil-plugin-confluence@latest",
		Binary:      "sigil-plugin-confluence",
		EnvVars: []EnvVarSpec{
			{Name: "CONFLUENCE_URL", Description: "Confluence instance URL", Required: true},
			{Name: "CONFLUENCE_EMAIL", Description: "Confluence account email", Required: true},
			{Name: "CONFLUENCE_TOKEN", Description: "Confluence API token", Required: true, Secret: true},
		},
	},
	{
		Name:        "notion",
		Description: "Notion docs, databases, task boards",
		Version:     "v2",
		Category:    "knowledge",
		Language:    "go",
		GoModule:    "github.com/alecfeeman/sigil-plugin-notion@latest",
		Binary:      "sigil-plugin-notion",
		EnvVars: []EnvVarSpec{
			{Name: "NOTION_TOKEN", Description: "Notion integration token", Required: true, Secret: true},
		},
	},
	{
		Name:        "slack",
		Description: "Task-related Slack messages and threads",
		Version:     "v2",
		Category:    "communication",
		Language:    "go",
		GoModule:    "github.com/alecfeeman/sigil-plugin-slack@latest",
		Binary:      "sigil-plugin-slack",
		EnvVars: []EnvVarSpec{
			{Name: "SLACK_TOKEN", Description: "Slack bot token", Required: true, Secret: true},
		},
	},
	{
		Name:        "gitlab",
		Description: "GitLab MRs, pipelines, issues",
		Version:     "v2",
		Category:    "scm",
		Language:    "go",
		GoModule:    "github.com/alecfeeman/sigil-plugin-gitlab@latest",
		Binary:      "sigil-plugin-gitlab",
		EnvVars: []EnvVarSpec{
			{Name: "GITLAB_URL", Description: "GitLab instance URL", Required: true},
			{Name: "GITLAB_TOKEN", Description: "GitLab personal access token", Required: true, Secret: true},
		},
	},
	{
		Name:        "jetbrains",
		Description: "JetBrains IDE active file, run configs, debug",
		Version:     "v2",
		Category:    "ide",
		Language:    "go",
		GoModule:    "github.com/alecfeeman/sigil-plugin-jetbrains@latest",
		Binary:      "sigil-plugin-jetbrains",
		HasSetup:    true,
	},
	{
		Name:        "github-actions",
		Description: "GitHub Actions workflow runs, status, timing",
		Version:     "v2",
		Category:    "ci",
		Language:    "go",
		GoModule:    "github.com/alecfeeman/sigil-plugin-github-actions@latest",
		Binary:      "sigil-plugin-github-actions",
		EnvVars: []EnvVarSpec{
			{Name: "GITHUB_TOKEN", Description: "GitHub token (reuses github plugin token)", Required: true, Secret: true},
		},
	},
	{
		Name:        "copilot",
		Description: "GitHub Copilot completions accepted/rejected",
		Version:     "v2",
		Category:    "ai",
		Language:    "go",
		GoModule:    "github.com/alecfeeman/sigil-plugin-copilot@latest",
		Binary:      "sigil-plugin-copilot",
		HasSetup:    true,
	},

	// ── v3: Observability & Deployment ─────────────────────────────────
	{Name: "sentry", Description: "Errors linked to commits", Version: "v3", Category: "observability", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-sentry@latest", Binary: "sigil-plugin-sentry"},
	{Name: "datadog", Description: "Metrics, monitors, alerts", Version: "v3", Category: "observability", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-datadog@latest", Binary: "sigil-plugin-datadog"},
	{Name: "pagerduty", Description: "On-call status, incidents", Version: "v3", Category: "observability", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-pagerduty@latest", Binary: "sigil-plugin-pagerduty"},
	{Name: "grafana", Description: "Dashboard alerts, annotations", Version: "v3", Category: "observability", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-grafana@latest", Binary: "sigil-plugin-grafana"},
	{Name: "argocd", Description: "Deployment status, sync state", Version: "v3", Category: "ci", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-argocd@latest", Binary: "sigil-plugin-argocd"},
	{Name: "vercel", Description: "Preview deploys, build status", Version: "v3", Category: "ci", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-vercel@latest", Binary: "sigil-plugin-vercel"},
	{Name: "docker", Description: "Container builds, image pushes", Version: "v3", Category: "ci", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-docker@latest", Binary: "sigil-plugin-docker"},
	{Name: "k8s", Description: "Pod status, logs, events", Version: "v3", Category: "observability", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-k8s@latest", Binary: "sigil-plugin-k8s"},

	// ── v4: Communication & Collaboration ──────────────────────────────
	{Name: "teams", Description: "Microsoft Teams messages, channels", Version: "v4", Category: "communication", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-teams@latest", Binary: "sigil-plugin-teams"},
	{Name: "discord", Description: "Dev community channels", Version: "v4", Category: "communication", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-discord@latest", Binary: "sigil-plugin-discord"},
	{Name: "google-calendar", Description: "Meeting blocks, focus time", Version: "v4", Category: "communication", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-google-calendar@latest", Binary: "sigil-plugin-google-calendar"},
	{Name: "outlook", Description: "Calendar, email threads", Version: "v4", Category: "communication", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-outlook@latest", Binary: "sigil-plugin-outlook"},
	{Name: "figma", Description: "Design files linked to stories", Version: "v4", Category: "knowledge", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-figma@latest", Binary: "sigil-plugin-figma"},

	// ── v5: Security, Quality & Dependencies ──────────────────────────
	{Name: "snyk", Description: "Vulnerability alerts on dependencies", Version: "v5", Category: "security", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-snyk@latest", Binary: "sigil-plugin-snyk"},
	{Name: "sonarqube", Description: "Code quality gates, coverage", Version: "v5", Category: "security", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-sonarqube@latest", Binary: "sigil-plugin-sonarqube"},
	{Name: "dependabot", Description: "Dependency PRs, security alerts", Version: "v5", Category: "security", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-dependabot@latest", Binary: "sigil-plugin-dependabot"},
	{Name: "codecov", Description: "Coverage diff on current branch", Version: "v5", Category: "security", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-codecov@latest", Binary: "sigil-plugin-codecov"},
	{Name: "semgrep", Description: "Static analysis findings", Version: "v5", Category: "security", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-semgrep@latest", Binary: "sigil-plugin-semgrep"},

	// ── vX: Extended Ecosystem ────────────────────────────────────────
	{Name: "bitbucket", Description: "Bitbucket PRs, pipelines", Version: "vX", Category: "scm", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-bitbucket@latest", Binary: "sigil-plugin-bitbucket"},
	{Name: "azure-devops", Description: "Azure DevOps boards, repos, pipelines", Version: "vX", Category: "scrum", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-azure-devops@latest", Binary: "sigil-plugin-azure-devops"},
	{Name: "shortcut", Description: "Shortcut stories, iterations", Version: "vX", Category: "scrum", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-shortcut@latest", Binary: "sigil-plugin-shortcut"},
	{Name: "asana", Description: "Asana tasks, projects", Version: "vX", Category: "scrum", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-asana@latest", Binary: "sigil-plugin-asana"},
	{Name: "clickup", Description: "ClickUp tasks, spaces", Version: "vX", Category: "scrum", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-clickup@latest", Binary: "sigil-plugin-clickup"},
	{Name: "monday", Description: "Monday.com items, boards", Version: "vX", Category: "scrum", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-monday@latest", Binary: "sigil-plugin-monday"},
	{Name: "trello", Description: "Trello cards, lists", Version: "vX", Category: "scrum", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-trello@latest", Binary: "sigil-plugin-trello"},
	{Name: "buildkite", Description: "Buildkite pipeline runs", Version: "vX", Category: "ci", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-buildkite@latest", Binary: "sigil-plugin-buildkite"},
	{Name: "circleci", Description: "CircleCI workflows, jobs", Version: "vX", Category: "ci", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-circleci@latest", Binary: "sigil-plugin-circleci"},
	{Name: "jenkins", Description: "Jenkins build status", Version: "vX", Category: "ci", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-jenkins@latest", Binary: "sigil-plugin-jenkins"},
	{Name: "terraform", Description: "Terraform plan/apply status", Version: "vX", Category: "ci", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-terraform@latest", Binary: "sigil-plugin-terraform"},
	{Name: "newrelic", Description: "APM data, error rates", Version: "vX", Category: "observability", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-newrelic@latest", Binary: "sigil-plugin-newrelic"},
	{Name: "opsgenie", Description: "Alerts, on-call schedules", Version: "vX", Category: "observability", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-opsgenie@latest", Binary: "sigil-plugin-opsgenie"},
	{Name: "neovim", Description: "Neovim buffer events, LSP", Version: "vX", Category: "ide", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-neovim@latest", Binary: "sigil-plugin-neovim"},
	{Name: "cursor", Description: "Cursor AI completions, chat", Version: "vX", Category: "ai", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-cursor@latest", Binary: "sigil-plugin-cursor"},
	{Name: "windsurf", Description: "Codeium/Windsurf completions", Version: "vX", Category: "ai", Language: "go", GoModule: "github.com/alecfeeman/sigil-plugin-windsurf@latest", Binary: "sigil-plugin-windsurf"},
	{Name: "aider", Description: "Aider conversations, edits", Version: "vX", Category: "ai", Language: "shell", GoModule: "", Binary: "sigil-plugin-aider"},
}
