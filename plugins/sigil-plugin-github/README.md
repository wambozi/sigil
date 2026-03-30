# sigil-plugin-github

Polls the GitHub API for PR status, CI checks, reviews, comments, and linked issues, then pushes structured events to sigild.

## How It Works

The plugin discovers git repos by scanning common project directories (`~/PycharmProjects`, `~/projects`, `~/code`, `~/src`, `~/workspace`, `~/dev`), reads GitHub remotes (origin + upstream), and polls the API for open PRs on the current branch.

## Data Collected

| Event Kind      | Description                                         |
|-----------------|-----------------------------------------------------|
| `pr_status`     | Open PR metadata: title, state, draft, mergeable, reviewers, labels |
| `pr_reviews`    | Review summary: approved/changes_requested/commented counts |
| `pr_comments`   | Inline code review comments (most recent 20)        |
| `pr_discussion` | General PR discussion comments (most recent 20)     |
| `ci_status`     | GitHub Actions check run results: success/failure/pending |
| `linked_issue`  | Issues referenced in PR title (#NNN): state, labels, milestone |

All events include `repo_root`, `branch`, and `pr_id` correlation fields. Comment bodies are truncated to 300 characters. Issue bodies are truncated to 500 characters.

## Health Endpoint

`GET http://127.0.0.1:7781/health` returns JSON with `status`, `error_count`, and `last_success`.

The plugin enters `degraded` status when:
- GitHub API returns 401 (token expired)
- Rate limit remaining drops below 100 (plugin sleeps until reset)

## Auth

Uses `GITHUB_TOKEN` env var. Falls back to `gh auth token` (GitHub CLI).

## Environment Variables

- `GITHUB_TOKEN` — GitHub personal access token.
- `SIGIL_INGEST_URL` — Override the sigild ingest endpoint (default: `http://127.0.0.1:7775/api/v1/ingest`).

## Flags

- `--poll-interval` — Poll interval (default: `2m`).
- `--watch-dirs` — Comma-separated directories to scan for git repos.
- `--sigil-ingest-url` — Sigil ingest URL.

## Actions

- `create-issue` — Create a GitHub issue.
- `comment-pr` — Add a comment to a PR.
- `close-issue` — Close a GitHub issue.
