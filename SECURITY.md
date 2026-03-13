# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest release | Yes |
| older releases | No |

Sigil is pre-1.0. Only the latest release receives security fixes.

## Reporting a Vulnerability

**Do not open a public issue for security vulnerabilities.**

Email **security@sigilos.io** with:

1. Description of the vulnerability
2. Steps to reproduce
3. Affected version(s)
4. Impact assessment (what an attacker could do)

You will receive an acknowledgment within 48 hours. We aim to provide a fix
or mitigation within 7 days for critical issues.

## Scope

Sigil runs as a user-space daemon with no elevated privileges. The primary
security surface includes:

- **Local SQLite database** (`~/.local/share/sigild/data.db`) — contains
  workflow telemetry. Protected by filesystem permissions.
- **Unix domain socket** (`/tmp/sigild.sock`) — IPC between `sigild` and
  `sigilctl`. No authentication beyond Unix socket permissions.
- **Inference engine** — when configured for cloud routing, sends prompts
  to configured API endpoints over HTTPS.
- **Fleet reporter** — when opted in, sends anonymized aggregate metrics
  to a configured fleet endpoint over HTTPS.

## Design Principles

- All data is local-first. Nothing leaves the machine without explicit opt-in.
- No network listeners — IPC is Unix socket only.
- No root/sudo required. Runs as the current user.
- Cloud API keys are read from environment variables, never persisted to disk
  by the daemon.
- Fleet telemetry is anonymized and aggregated before transmission.
