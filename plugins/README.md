# Sigil Plugins

Plugins extend sigild with data from external systems — project management, AI tools, CI/CD, communication, observability, and more. They run as standalone processes and push events to sigild via HTTP.

## Architecture

```
sigild
  └── plugin manager
       ├── starts/stops/monitors plugin processes
       └── HTTP ingest server (localhost:7775)
            ↑              ↑              ↑
       ┌────┴───┐    ┌────┴───┐    ┌────┴───┐
       │  jira  │    │ claude │    │ github │
       │ plugin │    │ plugin │    │ plugin │
       └────────┘    └────────┘    └────────┘
```

## Plugin Contract

A plugin is any executable that:

1. Accepts `--sigil-ingest-url <url>` to know where to POST events
2. POSTs JSON events to that URL (`POST /api/v1/ingest`)
3. Exits cleanly on SIGTERM
4. Optionally exposes `/health` for sigild monitoring

## Event Format

```json
{
  "plugin": "jira",
  "kind": "story_update",
  "timestamp": "2026-03-18T14:00:00Z",
  "correlation": {
    "story_id": "PROJ-123",
    "branch": "feat/auth",
    "repo_root": "/home/user/projects/myapp",
    "pr_id": "456"
  },
  "payload": {
    "title": "Add OAuth login flow",
    "status": "in_progress",
    "acceptance_criteria": ["OAuth redirect works", "Token refresh implemented"]
  }
}
```

The `correlation` object links events to Sigil tasks via branch name, story ID, or PR ID. Sigil's task tracker uses this to enrich its understanding of what the engineer is working on and why.

Events can be sent individually or as a JSON array (batch).

## Configuration

```toml
[plugins.jira]
enabled = true
binary = "sigil-plugin-jira"
poll_interval = "5m"
health_url = "http://127.0.0.1:7780/health"

[plugins.jira.env]
JIRA_URL = "https://mycompany.atlassian.net"
JIRA_EMAIL = "alec@company.com"
JIRA_TOKEN = ""  # or set SIGIL_JIRA_TOKEN env var

[plugins.claude]
enabled = true
binary = "sigil-plugin-claude"

[plugins.github]
enabled = true
binary = "sigil-plugin-github"
poll_interval = "2m"

[plugins.github.env]
GITHUB_TOKEN = ""
```

## Writing a Plugin

### Go (preferred)

```go
package main

import (
    "bytes"
    "encoding/json"
    "flag"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"
)

type Event struct {
    Plugin      string         `json:"plugin"`
    Kind        string         `json:"kind"`
    Timestamp   time.Time      `json:"timestamp"`
    Correlation map[string]any `json:"correlation"`
    Payload     map[string]any `json:"payload"`
}

func main() {
    ingestURL := flag.String("sigil-ingest-url", "http://127.0.0.1:7775/api/v1/ingest", "Sigil ingest URL")
    flag.Parse()

    // Set up graceful shutdown.
    stop := make(chan os.Signal, 1)
    signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()

    for {
        select {
        case <-stop:
            return
        case <-ticker.C:
            events := poll() // your logic here
            send(*ingestURL, events)
        }
    }
}

func send(url string, events []Event) {
    body, _ := json.Marshal(events)
    http.Post(url, "application/json", bytes.NewReader(body))
}
```

### Shell

```bash
#!/bin/bash
# Simple plugin that POSTs events to sigild
INGEST_URL="${1:-http://127.0.0.1:7775/api/v1/ingest}"

curl -s -X POST "$INGEST_URL" \
  -H "Content-Type: application/json" \
  -d '{
    "plugin": "my-plugin",
    "kind": "custom_event",
    "timestamp": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
    "correlation": {},
    "payload": {"message": "hello from my plugin"}
  }'
```

## CLI Commands

```bash
sigilctl plugin list          # show registered plugins and status
sigilctl plugin events        # recent plugin events
sigilctl plugin events jira   # events from a specific plugin
```

---

## Plugin Roadmap

### v1 — Core Dev Loop

| Plugin | Language | Source | Data |
|--------|----------|--------|------|
| **sigil-plugin-claude** | Go | Claude Code hooks | AI prompts, tool calls, token usage, accept/reject |
| **sigil-plugin-jira** | Go | Jira REST API | Stories, acceptance criteria, status, sprint, priority, assignee |
| **sigil-plugin-github** | Go | GitHub REST/GraphQL | PRs, CI status, reviews, comments, issue links |
| **sigil-plugin-vscode** | Go | VS Code extension API | Active file, debug sessions, terminal, extensions |

### v2 — Full Team Workflow

| Plugin | Language | Source | Data |
|--------|----------|--------|------|
| **sigil-plugin-linear** | Go | Linear GraphQL API | Issues, cycles, project status, labels |
| **sigil-plugin-confluence** | Go | Confluence REST API | Pages linked to stories, spec content |
| **sigil-plugin-notion** | Go | Notion API | Docs, databases, task boards |
| **sigil-plugin-slack** | Go | Slack Events API | Task-related messages, threads, blockers |
| **sigil-plugin-gitlab** | Go | GitLab REST API | MRs, pipelines, issues, reviews |
| **sigil-plugin-jetbrains** | Go | JetBrains plugin API | Active file, run configs, debug sessions |
| **sigil-plugin-github-actions** | Go | GitHub Actions API | Workflow runs, step status, logs, timing |
| **sigil-plugin-copilot** | Go | VS Code extension telemetry | Completions accepted/rejected |

### v3 — Observability & Deployment

| Plugin | Language | Source | Data |
|--------|----------|--------|------|
| **sigil-plugin-sentry** | Go | Sentry API | Errors linked to commits, stack traces |
| **sigil-plugin-datadog** | Go | Datadog API | Metrics, monitors, alerts tied to services |
| **sigil-plugin-pagerduty** | Go | PagerDuty API | On-call status, incidents |
| **sigil-plugin-grafana** | Go | Grafana API | Dashboard alerts, annotations |
| **sigil-plugin-argocd** | Go | ArgoCD API | Deployment status, sync state, rollbacks |
| **sigil-plugin-vercel** | Go | Vercel API | Preview deploys, build status |
| **sigil-plugin-docker** | Go | Docker daemon API | Container builds, image pushes |
| **sigil-plugin-k8s** | Go | Kubernetes API | Pod status, logs, events |

### v4 — Communication & Collaboration

| Plugin | Language | Source | Data |
|--------|----------|--------|------|
| **sigil-plugin-teams** | Go | Microsoft Teams API | Messages, channels, meeting transcripts |
| **sigil-plugin-discord** | Go | Discord API | Dev community channels |
| **sigil-plugin-google-calendar** | Go | Google Calendar API | Meeting blocks, focus time |
| **sigil-plugin-outlook** | Go | Microsoft Graph API | Calendar, email threads |
| **sigil-plugin-loom** | Go | Loom API | Video recordings linked to PRs |
| **sigil-plugin-figma** | Go | Figma API | Design files linked to stories |

### v5 — Security, Quality & Dependencies

| Plugin | Language | Source | Data |
|--------|----------|--------|------|
| **sigil-plugin-snyk** | Go | Snyk API | Vulnerability alerts on touched dependencies |
| **sigil-plugin-sonarqube** | Go | SonarQube API | Code quality gates, coverage |
| **sigil-plugin-dependabot** | Go | GitHub API | Dependency PRs, security alerts |
| **sigil-plugin-codecov** | Go | Codecov API | Coverage diff on current branch |
| **sigil-plugin-semgrep** | Go | Semgrep API | Static analysis findings |

### vX — Extended Ecosystem

| Plugin | Language | Source |
|--------|----------|--------|
| **sigil-plugin-bitbucket** | Go | Bitbucket API |
| **sigil-plugin-azure-devops** | Go | Azure DevOps API |
| **sigil-plugin-shortcut** | Go | Shortcut API |
| **sigil-plugin-asana** | Go | Asana API |
| **sigil-plugin-clickup** | Go | ClickUp API |
| **sigil-plugin-monday** | Go | Monday.com API |
| **sigil-plugin-trello** | Go | Trello API |
| **sigil-plugin-buildkite** | Go | Buildkite API |
| **sigil-plugin-circleci** | Go | CircleCI API |
| **sigil-plugin-jenkins** | Go | Jenkins API |
| **sigil-plugin-terraform** | Go | Terraform Cloud API |
| **sigil-plugin-newrelic** | Go | New Relic API |
| **sigil-plugin-opsgenie** | Go | OpsGenie API |
| **sigil-plugin-toggl** | Go | Toggl API |
| **sigil-plugin-neovim** | Go | Neovim RPC |
| **sigil-plugin-cursor** | Go | Cursor telemetry |
| **sigil-plugin-windsurf** | Go | Codeium/Windsurf API |
| **sigil-plugin-aider** | Shell | Aider CLI wrapper |
| **sigil-plugin-storybook** | Go | Storybook API |
| **sigil-plugin-chromatic** | Go | Chromatic API |
| **sigil-plugin-gitbook** | Go | GitBook API |
| **sigil-plugin-coda** | Go | Coda API |
| **sigil-plugin-zulip** | Go | Zulip API |

## Plugin Priority by Team Type

| Team | v1 Must-Have | v2 Priority |
|------|-------------|-------------|
| **Startup (GitHub + Linear)** | claude, github, vscode | linear, notion, slack |
| **Enterprise (Atlassian)** | claude, jira, github, vscode | confluence, teams, gitlab |
| **Open Source** | claude, github, vscode | discord, sentry |
| **Platform / SRE** | claude, github, vscode | k8s, datadog, pagerduty, argocd |
| **Frontend** | claude, github, vscode | figma, vercel, storybook |

## Privacy

Plugin events are stored locally in sigild's SQLite database. The fleet reporter only sends anonymized aggregates — individual story titles, PR descriptions, or AI prompts are never transmitted. Each plugin documents exactly what data it collects in its own README.
