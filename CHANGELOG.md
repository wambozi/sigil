# Changelog

## [0.1.0-beta] - 2026-03-22

### Added
- Core daemon (`sigild`) with background workflow observation
- CLI client (`sigilctl`) with 10+ commands
- 5 event sources: file, process, git, terminal, Hyprland
- SQLite store with WAL mode and 90-day retention
- 15 heuristic pattern detectors in the analyzer
- Hybrid inference engine (local llama.cpp + cloud Anthropic/OpenAI)
- 4 inference routing modes: local, localfirst, remotefirst, remote
- 5-level notification system (Silent -> Autonomous)
- Reversible action registry (actuator)
- Fleet telemetry aggregation (opt-in, anonymized)
- TCP+TLS network listener for remote shell connections
- Unix socket JSON API with 30+ methods
- VS Code extension for IDE suggestion toasts
- Plugin system with 5 plugins (Claude, GitHub, JetBrains, Jira, VS Code)
- Task tracker with phase detection
- ML prediction sidecar integration
- Shell hooks for Zsh and Bash

### Security
- All data stored locally, never sent without user review
- TLS with SPKI fingerprint pinning for network connections
- Credential management via `sigilctl credential` commands
