# sigil-plugin-jira

Polls the Jira REST API for stories assigned to the current user and pushes structured data to sigild.

## How It Works

The plugin queries Jira for issues assigned to the current user that are not in a "Done" status category. For each issue, it fetches comments, available transitions, and active sprint context.

## Data Collected

| Event Kind          | Description                                              |
|---------------------|----------------------------------------------------------|
| `story`             | Issue metadata: key, summary, description, status, priority, type, labels, subtasks, sprint, epic, acceptance criteria |
| `story_comments`    | Recent comments on the issue (most recent 10)            |
| `story_transitions` | Available workflow transitions for the issue             |
| `sprint`            | Active sprint info: name, goal, start/end dates          |

Descriptions are truncated to 500 characters. Comment bodies are truncated to 300 characters. Jira's Atlassian Document Format is automatically converted to plain text.

## Health Endpoint

`GET http://127.0.0.1:7782/health` returns JSON with `status`, `error_count`, and `last_success`.

The plugin enters `degraded` status on 401 Unauthorized (credentials expired). Server errors (5xx) are retried up to 3 times with exponential backoff.

## Auth

Requires three environment variables:
- `JIRA_URL` — Jira instance URL (e.g. `https://yourteam.atlassian.net`).
- `JIRA_EMAIL` — Jira account email.
- `JIRA_TOKEN` — Jira API token.

## Environment Variables

- `JIRA_URL` — Jira instance URL.
- `JIRA_EMAIL` — Jira account email.
- `JIRA_TOKEN` — Jira API token.
- `SIGIL_INGEST_URL` — Override the sigild ingest endpoint (default: `http://127.0.0.1:7775/api/v1/ingest`).

## Flags

- `--poll-interval` — Poll interval (default: `5m`).
- `--sigil-ingest-url` — Sigil ingest URL.
