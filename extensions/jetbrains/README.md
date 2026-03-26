# Sigil JetBrains Extension

IntelliJ Platform plugin that surfaces [sigild](../../README.md) workflow suggestions in JetBrains IDEs (IntelliJ IDEA, PyCharm, GoLand, WebStorm, and others).

## Features

- Connects to the local sigild daemon via Unix domain socket
- Subscribes to the suggestions push topic for real-time notifications
- Displays suggestions as IDE notification balloons with Accept/Dismiss actions
- Status bar widget shows connection state (Connected / Disconnected)
- "Sigil: Show Suggestions" action in Find Action (Ctrl+Shift+A / Cmd+Shift+A)
- Auto-reconnect with exponential backoff (1s to 30s)
- Desktop notifications are automatically suppressed when the extension is connected (single-channel routing via `HasExternalSurface`)

## Requirements

- JetBrains IDE 2024.1 or later (build 241+)
- Java 17+ (for Unix domain socket support)
- sigild daemon running locally

## Setup

Generate the Gradle wrapper (requires Gradle 8.5+ installed):

```bash
cd extensions/jetbrains
gradle wrapper --gradle-version 8.5
```

Or install Gradle via your package manager:

```bash
# macOS
brew install gradle

# Linux (SDKMAN)
sdk install gradle 8.5
```

## Build

```bash
cd extensions/jetbrains
./gradlew buildPlugin
```

The plugin ZIP is produced at `build/distributions/sigil-jetbrains-0.1.0.zip`.

## Install

1. Build the plugin (see above)
2. In your JetBrains IDE, go to **Settings > Plugins > Gear icon > Install Plugin from Disk...**
3. Select `build/distributions/sigil-jetbrains-0.1.0.zip`
4. Restart the IDE

## Development

Run the plugin in a sandboxed IDE instance:

```bash
./gradlew runIde
```

## Architecture

| File | Purpose |
|------|---------|
| `SigilClient.kt` | Unix domain socket client with subscribe + RPC |
| `SigilStartupActivity.kt` | Connects to sigild on project open |
| `SigilNotificationHandler.kt` | Suggestion balloons with Accept/Dismiss feedback |
| `SigilStatusBarWidget.kt` | Connected/Disconnected indicator in status bar |
| `SigilStatusBarWidgetFactory.kt` | Factory for the status bar widget |
| `SigilShowSuggestionsAction.kt` | Browse suggestion history via Find Action |
| `SigilKeys.kt` | Shared project-level UserData keys |

## Socket Protocol

The plugin communicates with sigild using the same JSON-over-Unix-socket protocol as sigilctl and the VS Code extension:

- **Subscribe**: `{"method": "subscribe", "payload": {"topic": "suggestions"}}`
- **RPC**: `{"method": "suggestions"}` or `{"method": "feedback", "payload": {"suggestion_id": 1, "outcome": "accepted"}}`
- **Push events**: `{"event": "suggestions", "payload": {...}}`

## Privacy

The extension communicates only with the local sigild Unix socket. No data is sent to external servers.
