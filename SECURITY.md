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

## TCP Network Listener (optional)

sigild can optionally accept remote connections over TCP+TLS. This is disabled
by default and must be explicitly enabled in the daemon configuration.

### Configuration

```toml
[network]
enabled = true
bind    = "0.0.0.0"
port    = 7773
```

### Threat model

- **Disabled by default.** No TCP port is opened unless `network.enabled = true`.
- **Port.** Default port 7773. Firewall rules are the operator's responsibility.
- **Transport security.** TLS 1.3 (minimum). TLS 1.2 and below are rejected.
  The server presents a self-signed ECDSA P-256 certificate generated at first
  start. The certificate is stored at `$XDG_DATA_HOME/sigil/server-cert.pem`.
- **Authentication model.** Bearer token — one token per remote client identity.
  The client must send `{"method":"auth","payload":{"token":"..."}}` as the
  first message after the TLS handshake. No daemon method is dispatched before
  successful authentication.
- **MITM protection.** Clients verify the server's TLS certificate via SPKI
  fingerprint pinning (`sha256/<base64>` of `SubjectPublicKeyInfo`). The
  fingerprint is baked into the credential file transferred out-of-band.
  Standard CA chain validation is not used.
- **Token storage.** Bearer tokens are stored on disk as SHA-256 hex hashes.
  Plaintext tokens are only visible at credential creation time (output of
  `sigilctl credential add`).
- **Credential revocation.** Immediate and hot — no daemon restart required.
  `sigilctl credential revoke <name>` removes the credential from the in-memory
  store and persists the change to disk.

### Credential lifecycle

1. Run `sigilctl credential add <name>` on the daemon machine.
2. Copy the JSON output to the remote host and save with `chmod 600`.
3. Configure sigil-shell with `"transport":"tcp"` pointing at the credential file.
4. To revoke: run `sigilctl credential revoke <name>` and delete the credential
   file on the remote host.

## Design Principles

- All data is local-first. Nothing leaves the machine without explicit opt-in.
- Unix socket IPC is the default; TCP listener requires opt-in configuration.
- No root/sudo required. Runs as the current user.
- Cloud API keys are read from environment variables, never persisted to disk
  by the daemon.
- Fleet telemetry is anonymized and aggregated before transmission.
