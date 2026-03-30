# sigil-plugin-vscode

Monitors VS Code state by reading its local storage files and pushes events to sigild. No extension installation required.

## How It Works

The plugin polls VS Code's state files on disk to detect workspace changes, recently opened files, and installed extensions. It does not require a running VS Code instance or extension installation.

## Data Collected

| Event Kind          | Description                                              |
|---------------------|----------------------------------------------------------|
| `workspace_change`  | Active workspace changed (path, total recent workspaces) |
| `recent_files`      | Newly opened files since last poll (up to 10)            |
| `extensions`        | Installed VS Code extensions matching interesting keywords (AI tools, language servers, debuggers, linters) |

The plugin tracks recently opened files in memory and only emits new files. Old entries are pruned after 1 hour.

## Health Endpoint

`GET http://127.0.0.1:7783/health` returns JSON with `status`, `error_count`, and `last_success`.

## Auth

No auth required. The plugin reads local VS Code state files.

## Supported Platforms

- **macOS**: `~/Library/Application Support/Code/User/globalStorage/`
- **Linux**: `~/.config/Code/User/globalStorage/`
- **Windows**: Not currently supported.

## Environment Variables

- `SIGIL_INGEST_URL` — Override the sigild ingest endpoint (default: `http://127.0.0.1:7775/api/v1/ingest`).

## Flags

- `--poll-interval` — Poll interval (default: `30s`).
- `--sigil-ingest-url` — Sigil ingest URL.
