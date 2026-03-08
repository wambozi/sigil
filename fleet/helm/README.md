# Sigil Fleet Helm Chart

Deploy the Fleet Aggregation Layer on Kubernetes.

## Quick Start

```bash
helm install sigil-fleet ./fleet/helm \
  --set secrets.apiKey=YOUR_API_KEY \
  --set config.dbURL=postgres://user:pass@host:5432/sigil_fleet
```

## Configuration

| Parameter | Description | Default |
|---|---|---|
| `replicaCount` | Number of replicas | `1` |
| `config.dbURL` | PostgreSQL connection string | `postgres://sigil:changeme@postgresql:5432/sigil_fleet?sslmode=disable` |
| `config.listenAddr` | HTTP listen address | `:8090` |
| `config.cloudCostPerQuery` | Cost per cloud AI query ($) | `0.01` |
| `config.oidcIssuer` | OIDC issuer URL (optional) | `""` |
| `config.oidcClientID` | OIDC client ID | `""` |
| `config.oidcAdminGroup` | OIDC group for admin access | `sigil-admins` |
| `secrets.apiKey` | API key for report ingestion | `""` |
| `secrets.oidcClientSecret` | OIDC client secret | `""` |
| `postgresql.enabled` | Deploy PostgreSQL subchart | `true` |

## With External PostgreSQL

```bash
helm install sigil-fleet ./fleet/helm \
  --set postgresql.enabled=false \
  --set config.dbURL=postgres://user:pass@your-db:5432/sigil_fleet
```
