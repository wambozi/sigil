# Sigil for VS Code

A persistent sidebar panel that connects to the [sigild](../../) daemon, providing real-time suggestions, daemon status monitoring, and event inspection.

## Install

Build the `.vsix` and install it:

```bash
cd extensions/vscode
npm install
npm run compile
npm run package
code --install-extension sigil-vscode-0.2.0.vsix
```

## Usage

1. Start `sigild` (the Sigil daemon)
2. Open VS Code — the extension activates automatically
3. Look for the **Sigil** icon in the activity bar (right sidebar)
4. The panel has three tabs:

### Suggestions

Real-time suggestions appear as expandable cards. Each card shows the title, confidence score, and timestamp. Click to expand for full details, then **Accept** or **Dismiss** inline.

Two display modes (toggle in the toolbar):
- **Timeline**: All suggestions, newest first
- **Active List**: Only pending suggestions, sorted by confidence

### Status & Metrics

Live view of daemon health:
- Connection state, version, uptime
- Notification level, routing mode, analysis interval
- `sigild` RSS memory usage
- `llama-server` resource utilization (when a local model is active): RSS, CPU%, model name, context window usage

Refreshes automatically at a configurable interval.

### Events

Browse raw telemetry events stored by the daemon:
- Filter by source type (file, process, git, terminal, hyprland)
- Paginated table (100 events per page)
- Click any row to expand and view the full event payload
- Select events and **Purge Selected**, or **Purge All Matching** to delete by filter

## Configuration

| Setting | Default | Description |
|---------|---------|-------------|
| `sigil.socketPath` | Auto-detected | Path to the sigild Unix socket |
| `sigil.metricsRefreshInterval` | `10000` | Status/metrics poll interval (ms, minimum 2000) |
| `sigil.suggestionDisplayMode` | `timeline` | `timeline` or `active-list` |

The socket path is auto-detected from `$XDG_RUNTIME_DIR/sigild.sock`. Override via VS Code settings if your daemon uses a non-standard path.

## How it works

The extension connects to the `sigild` Unix socket and subscribes to real-time suggestion push events. When connected, desktop notifications (`notify-send`) are automatically suppressed — only one notification channel is active at a time.

The panel communicates with the daemon via the socket JSON protocol, calling `status`, `metrics`, `events`, `event-detail`, `purge-events`, and `feedback` methods.
