# sigil-plugin-claude

Collects AI interaction data from Claude Code and can launch Claude Code sessions with context when Sigil recommends a task.

## Modes

- **hook** (default): Invoked by Claude Code hooks, captures tool calls and sends them to sigild.
- **install**: Adds hooks to `~/.claude/settings.json`.
- **launch**: Starts a Claude Code session with a prompt (called by sigild actuators).
- **capabilities**: Prints plugin capabilities as JSON.

## Data Collected

| Event Kind     | Description                                    |
|----------------|------------------------------------------------|
| `tool_use`     | Tool name, session ID, command/file/pattern    |
| `tool_result`  | Completed tool call with output                |
| `tool_error`   | Tool invocation that returned an error         |
| `notification` | Assistant notification (no tool name)           |
| `raw_input`    | Unparseable hook input (fallback)              |
| `session_launch` | Claude Code session launched via actuator    |

All events include a timestamp and optional `repo_root` correlation. Tool inputs are truncated (commands to 200 chars, patterns to 100 chars) to avoid sending large payloads.

## Health Endpoint

`GET http://127.0.0.1:7780/health` returns JSON with `status`, `error_count`, and `last_success`.

## Auth

No auth required. The plugin is invoked by Claude Code hooks.

## Environment Variables

- `SIGIL_INGEST_URL` — Override the sigild ingest endpoint (default: `http://127.0.0.1:7775/api/v1/ingest`).
