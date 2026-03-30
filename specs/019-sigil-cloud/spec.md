# 019 — Sigil Cloud Platform

**Status:** Draft
**Author:** Alec Feeman
**Date:** 2026-03-30

---

## Problem

Sigil is a local-first daemon. Everything runs on the user's machine: event collection, pattern detection, ML predictions (via sigil-ml sidecar), and LLM inference (via local models or user-provided API keys). This is the right default — it preserves privacy, works offline, and has zero operational cost.

But local-only has three gaps:

1. **Python dependency for ML:** sigil-ml requires a Python runtime, PyTorch, and ~2GB of model weights. Many users — especially on constrained machines or in corporate environments with restricted software policies — cannot or will not install a Python sidecar. They lose all ML-powered features (task prediction, workflow clustering, productivity scoring).

2. **No team visibility:** Individual developers get personal insights, but engineering managers and team leads have no way to see aggregate patterns across a team. Fleet metrics exist (spec 009) but they are anonymized and org-wide — there is no team-scoped dashboard with per-member opt-in visibility.

3. **LLM cost friction:** Users who want cloud LLM enrichment must bring their own API keys, configure provider credentials, and manage billing directly with Anthropic/OpenAI. This works for power users but is a barrier for casual adoption and impossible to manage at team scale.

A cloud platform solves all three by offering hosted ML inference (no Python needed), managed LLM access (no API keys needed), team dashboards (opt-in data sync), and centralized billing. The local daemon remains the product — the cloud is an optional accelerator.

## Solution

A separate repository (`sigil-tech/sigil-cloud`) containing all server-side infrastructure for the Sigil Cloud platform. The cloud platform provides hosted ML predictions, managed LLM inference, event sync for team dashboards, and Stripe-based billing across three tiers (Free, Pro, Team).

The daemon-side code (fleet reporter, sync agent, config structs) stays in the `sigil` repo. The cloud repo owns all server-side services, the user-facing dashboard, and deployment infrastructure.

### Prerequisites

This spec depends on:
- **Spec 009 (Fleet Metrics):** Fleet aggregation service and dashboard are extracted from the sigil monorepo into sigil-cloud
- **Spec 012 (sigil-ml):** Cloud ML proxy routes predictions to hosted sigil-ml instances
- **PR #60 (Cloud LLM Proxy):** LLM proxy code exists in the sigil repo and is extracted to sigil-cloud

## Requirements

### Auth & Billing Service — MUST

1. User registration MUST support email/password and OAuth (GitHub, Google) login flows
2. The service MUST integrate with Stripe for Pro and Team tier billing (monthly and annual)
3. The service MUST issue and rotate API keys for authenticating daemon-to-cloud communication
4. The service MUST support org/team management for Team tier: create org, invite members, assign roles (owner, admin, member)
5. The service MUST issue JWT tokens used by all downstream cloud APIs
6. The service MUST enforce tier entitlements: Free users MUST NOT access cloud ML or cloud LLM endpoints
7. API key rotation MUST NOT cause downtime — both old and new keys MUST be valid during a configurable grace period (default 24 hours)

### Auth & Billing Service — SHOULD

8. The service SHOULD support SSO/SAML for enterprise Team customers
9. The service SHOULD send email notifications for billing events (payment failed, trial ending, usage threshold)

### API Gateway — MUST

10. The gateway MUST route requests to backend services (auth, ingest, fleet, ml-proxy, llm-proxy) based on URL path prefix
11. The gateway MUST validate JWT tokens on all authenticated endpoints
12. The gateway MUST enforce per-tier rate limits: Pro at 1,000 requests/hour, Team at 10,000 requests/hour
13. The gateway MUST reject requests from Free-tier users to cloud ML and cloud LLM endpoints with HTTP 403 and a clear upgrade message
14. The gateway MUST add tenant context (user ID, org ID, tier) to forwarded requests via headers
15. The gateway MUST terminate TLS

### API Gateway — SHOULD

16. The gateway SHOULD support request/response logging for debugging (opt-in, not default)
17. The gateway SHOULD expose Prometheus metrics for request rate, latency, and error rate per service

### Event Ingest Service — MUST (EXISTS — extract from sigil/cloud/ingest/)

18. The service MUST accept batched events from sigild sync agents over HTTPS
19. The service MUST store events in per-tenant Postgres schemas for data isolation
20. The service MUST perform idempotent writes — replaying the same batch MUST NOT create duplicates
21. The service MUST validate event payloads against the `event.Event` schema before insertion
22. The service MUST reject events from unauthenticated or unauthorized sync agents

### Event Ingest Service — SHOULD

23. The service SHOULD support configurable retention per tenant (default 90 days for Pro, 365 days for Team)
24. The service SHOULD compress event payloads in transit (gzip)

### Fleet Aggregation Service — MUST (EXISTS — extract from sigil/fleet/service/)

25. The service MUST collect anonymized metrics from fleet reporters
26. The service MUST serve dashboard query endpoints for the 7 existing metric views (adoption, velocity, cost, compliance, tasks, quality, ML)
27. The service MUST distribute routing policies to fleet reporters
28. The service MUST authenticate requests via JWT (replacing the existing standalone JWT auth)

### Cloud ML Proxy — MUST (NEW)

29. The proxy MUST route prediction requests from sigild to hosted sigil-ml instances
30. The proxy MUST support tenant-aware model selection: personal model (trained on user's synced data) or aggregate model (trained on anonymized team data)
31. The proxy MUST cache hot models in memory and lazy-load cold models from S3
32. The proxy MUST track prediction latency per request for SLA monitoring
33. The proxy MUST return predictions in the same response format as the local sigil-ml sidecar so sigild requires zero code changes to use cloud ML
34. The proxy MUST enforce tier access: Pro gets personal model only, Team gets personal + aggregate models

### Cloud ML Proxy — SHOULD

35. The proxy SHOULD auto-scale hosted sigil-ml instances based on request volume
36. The proxy SHOULD support model versioning — rollback to previous model version on degraded accuracy

### Cloud LLM Proxy — MUST (EXISTS in sigil as PR #60 — extract)

37. The proxy MUST route LLM completion requests to configured providers (Anthropic, OpenAI)
38. The proxy MUST support per-user API key management: users can optionally bring their own keys, otherwise use the platform's pooled keys
39. The proxy MUST meter usage per user (token counts) for billing
40. The proxy MUST enforce per-tier token budgets: Pro at 500K tokens/month, Team at 2M tokens/month per seat
41. The proxy MUST support provider failover: if the primary provider is down, route to the secondary

### Cloud LLM Proxy — SHOULD

42. The proxy SHOULD cache common completions (e.g., pattern descriptions) to reduce provider costs
43. The proxy SHOULD support streaming responses for interactive use cases (Ask Sigil in tray app)

### User Dashboard — MUST (NEW — extends fleet/dashboard/)

44. The dashboard MUST show personal analytics: suggestion acceptance rate, productivity trends over time, task pattern breakdown
45. The dashboard MUST show cloud service status for Pro/Team users: ML prediction count, LLM token usage, sync status
46. The dashboard MUST show billing information: current plan, usage vs limits, upgrade/downgrade options
47. The dashboard MUST provide settings management: API key display/rotation, sync preferences (on/off, which data), data retention controls
48. The dashboard MUST provide an onboarding flow: guided setup for connecting a local sigild instance to the cloud platform (API key generation, config snippet for `config.toml`)
49. The dashboard MUST be responsive (usable on tablet and mobile viewports)

### User Dashboard — SHOULD

50. The dashboard SHOULD show team analytics for Team tier: team member list, aggregate acceptance rate, team productivity trends, top suggestion categories
51. The dashboard SHOULD show member management for Team tier: invite members, assign roles, remove members
52. The dashboard SHOULD support dark/light theme via `prefers-color-scheme`

### Admin Console — MUST (NEW)

53. The admin console MUST provide org management: list orgs, view org details, usage metrics
54. The admin console MUST provide usage monitoring: per-user and per-org request counts, token usage, storage consumption
55. The admin console MUST provide policy management: configure allowed LLM models per org, set custom rate limits
56. The admin console MUST provide audit logs: authentication events, API key rotations, billing changes, data exports

### Admin Console — SHOULD

57. The admin console SHOULD provide billing overview: revenue by tier, churn metrics, payment failure alerts
58. The admin console SHOULD support impersonation for customer support (view dashboard as a specific user, read-only)

### Infrastructure — MUST

59. Helm charts MUST be provided for all services with configurable replicas, resource limits, and environment variables
60. Postgres MUST be deployed as a managed service (RDS) or StatefulSet with automated backups
61. Redis MUST be deployed for rate limiting state and model cache metadata
62. S3 MUST be used for ML model artifact storage
63. Ingress MUST be configured with TLS termination and automatic certificate renewal (cert-manager)
64. All services MUST expose health check endpoints (`/healthz` for liveness, `/readyz` for readiness)

### Infrastructure — SHOULD

65. Terraform configurations SHOULD be provided for AWS infrastructure (RDS, ElastiCache, S3, EKS)
66. Database migrations SHOULD be managed by a versioned migration tool (golang-migrate or similar)

### CI/CD — MUST

67. GitHub Actions workflows MUST build, test, and push Docker images for all services on merge to main
68. A staging environment MUST exist for pre-production validation
69. Database migrations MUST run automatically as part of deployment
70. Docker images MUST be tagged with both the Git SHA and semantic version

### CI/CD — SHOULD

71. A staging-to-production promotion workflow SHOULD require manual approval
72. Canary deployments SHOULD be supported for backend services

### MUST NOT

73. The cloud platform MUST NOT store raw event data from Free-tier users — Free tier is local-only
74. The cloud platform MUST NOT access, read, or process any data from a user's local sigild instance unless the user has explicitly enabled data sync in their `config.toml`
75. The cloud platform MUST NOT retain user data beyond the configured retention period — expired data MUST be automatically purged
76. The cloud platform MUST NOT share any user's data with other users or orgs, even in anonymized form, without explicit opt-in to the aggregate model program
77. The admin console MUST NOT allow modification of user data — it is read-only for monitoring and support
78. API keys MUST NOT be stored in plaintext — they MUST be hashed (bcrypt or argon2) with only a prefix shown in the dashboard for identification

## Tier System

| Feature | Free | Pro ($15/mo) | Team ($25/seat/mo) |
|---------|------|-------------|-------------------|
| Local daemon | Yes | Yes | Yes |
| Local ML (sigil-ml sidecar) | Requires Python | Optional (can use cloud) | Optional (can use cloud) |
| Cloud ML predictions | No | Personal model | Personal + aggregate |
| Cloud LLM inference | No | 500K tokens/mo | 2M tokens/seat/mo |
| Data sync to cloud | No | Optional | Default (configurable) |
| Team dashboards | No | No | Yes |
| Aggregate team models | No | No | Yes |
| Personal analytics dashboard | No | Yes | Yes |
| API keys | No | 1 | Unlimited per org |
| Support | Community | Email | Priority |

### Tier Enforcement Points

| Enforcement Point | Mechanism |
|---|---|
| API Gateway | JWT claims include `tier` field; gateway middleware checks tier before routing to cloud ML/LLM |
| Auth Service | API key issuance checks tier; Free users cannot generate keys |
| Ingest Service | Rejects sync data from Free-tier agents |
| ML Proxy | Checks `tier` header; returns 403 for Free, allows personal model for Pro, personal+aggregate for Team |
| LLM Proxy | Checks `tier` header and token budget; returns 403 for Free, enforces monthly token limits |
| Dashboard | Frontend hides Pro/Team features; backend endpoints return 403 for unauthorized tiers |

## Success Criteria

- [ ] Auth service: register via email, login, receive JWT, generate API key
- [ ] Auth service: OAuth login with GitHub works end-to-end
- [ ] Auth service: Stripe checkout creates Pro subscription, tier reflected in JWT claims
- [ ] Gateway: routes to all 6 backend services correctly
- [ ] Gateway: rate limiting enforced per tier (verify with load test)
- [ ] Gateway: Free-tier requests to cloud ML/LLM return 403
- [ ] Ingest: sigild sync agent sends batch, events appear in tenant's Postgres schema
- [ ] Ingest: replay same batch — no duplicate rows
- [ ] Fleet: existing 7 dashboard views render with data from the extracted service
- [ ] ML Proxy: sigild sends prediction request, receives response in sigil-ml format
- [ ] ML Proxy: cold model loads from S3, subsequent requests served from cache
- [ ] LLM Proxy: sigild sends completion request, receives response from Anthropic/OpenAI
- [ ] LLM Proxy: usage metered correctly, reflects in billing dashboard
- [ ] Dashboard: onboarding flow generates API key and config snippet
- [ ] Dashboard: personal analytics show real suggestion data from synced events
- [ ] Dashboard: Team tier shows team member list and aggregate metrics
- [ ] Admin: audit log shows auth events and API key rotations
- [ ] CI/CD: merge to main builds and deploys to staging automatically
- [ ] All services pass health checks in Kubernetes
- [ ] End-to-end: fresh sigild instance connects to cloud, syncs events, receives cloud ML prediction, receives cloud LLM enrichment

## Entities & Data

### Database Schema (Postgres)

**Shared schema (`sigil_cloud`):**

| Table | Purpose |
|-------|---------|
| `users` | User accounts (id, email, password_hash, oauth_provider, oauth_id, created_at) |
| `orgs` | Organizations for Team tier (id, name, owner_id, stripe_customer_id, created_at) |
| `org_members` | Org membership (org_id, user_id, role: owner/admin/member, joined_at) |
| `api_keys` | Hashed API keys (id, user_id, key_hash, key_prefix, name, created_at, expires_at, revoked_at) |
| `subscriptions` | Stripe subscriptions (id, user_id, org_id, stripe_sub_id, tier, status, current_period_end) |
| `usage_records` | Usage metering (id, user_id, service: ml/llm, count, period_start, period_end) |
| `audit_log` | Audit events (id, user_id, action, resource, detail_json, ip, created_at) |

**Per-tenant schema (`tenant_{user_id}`):**

| Table | Purpose |
|-------|---------|
| `events` | Synced events from sigild (mirrors sigild's local event schema) |
| `suggestions` | Synced suggestions with outcomes |
| `task_sessions` | Inferred task sessions |

### External Integrations

| Integration | Purpose | Config |
|-------------|---------|--------|
| Stripe | Billing, subscriptions, webhooks | `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET` |
| Anthropic API | LLM completions (primary) | `ANTHROPIC_API_KEY` |
| OpenAI API | LLM completions (failover) | `OPENAI_API_KEY` |
| AWS S3 | ML model artifact storage | `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `S3_BUCKET` |
| SMTP / SendGrid | Transactional emails (welcome, billing) | `SMTP_HOST` or `SENDGRID_API_KEY` |

## Cloud Repo Structure

```
sigil-cloud/
├── services/
│   ├── auth/                   # Auth & billing service (Go)
│   │   ├── main.go
│   │   ├── handler/            # HTTP handlers (register, login, oauth, keys, orgs)
│   │   ├── stripe/             # Stripe integration (checkout, webhooks, metering)
│   │   ├── jwt/                # JWT issuance and validation
│   │   └── Dockerfile
│   ├── gateway/                # API gateway (Go)
│   │   ├── main.go
│   │   ├── middleware/         # JWT validation, rate limiting, tier enforcement
│   │   ├── proxy/              # Reverse proxy routing
│   │   └── Dockerfile
│   ├── ingest/                 # Event ingest (from sigil/cloud/ingest/)
│   │   ├── main.go
│   │   ├── handler/
│   │   ├── tenant/             # Per-tenant schema management
│   │   └── Dockerfile
│   ├── fleet/                  # Fleet aggregation (from sigil/fleet/service/)
│   │   ├── main.go
│   │   ├── handler/
│   │   ├── metrics/            # Metric computation for 7 views
│   │   └── Dockerfile
│   ├── ml-proxy/               # ML prediction routing (Go)
│   │   ├── main.go
│   │   ├── handler/
│   │   ├── cache/              # Model cache (Redis metadata, memory for hot models)
│   │   ├── s3/                 # Model artifact loading
│   │   └── Dockerfile
│   └── llm-proxy/              # LLM inference routing (from sigil/cloud/llm-proxy/)
│       ├── main.go
│       ├── handler/
│       ├── provider/           # Anthropic, OpenAI client wrappers
│       ├── metering/           # Token counting and usage tracking
│       └── Dockerfile
├── dashboard/
│   ├── src/
│   │   ├── main.tsx            # Preact entry
│   │   ├── App.tsx
│   │   ├── pages/
│   │   │   ├── Onboarding.tsx       # Guided cloud setup
│   │   │   ├── PersonalAnalytics.tsx # Suggestion stats, trends
│   │   │   ├── CloudStatus.tsx       # ML/LLM usage, sync status
│   │   │   ├── Billing.tsx           # Plan, usage, upgrade
│   │   │   ├── Settings.tsx          # API keys, sync, retention
│   │   │   ├── TeamDashboard.tsx     # Team-only: members, aggregates
│   │   │   └── Admin.tsx             # Admin console
│   │   ├── components/
│   │   │   ├── MetricCard.tsx
│   │   │   ├── TrendChart.tsx
│   │   │   ├── UsageBar.tsx
│   │   │   └── ApiKeyManager.tsx
│   │   └── lib/
│   │       ├── api.ts           # HTTP client for cloud APIs
│   │       └── auth.ts          # JWT management, login/logout
│   ├── package.json
│   ├── vite.config.ts
│   └── Dockerfile               # nginx serving static build
├── deploy/
│   ├── helm/
│   │   ├── sigil-cloud/         # Umbrella chart
│   │   ├── auth/
│   │   ├── gateway/
│   │   ├── ingest/
│   │   ├── fleet/
│   │   ├── ml-proxy/
│   │   └── llm-proxy/
│   ├── docker/
│   │   └── docker-compose.yml   # Local dev stack
│   └── terraform/
│       ├── main.tf              # AWS infra (EKS, RDS, ElastiCache, S3)
│       ├── variables.tf
│       └── outputs.tf
├── migrations/
│   ├── 001_create_users.up.sql
│   ├── 001_create_users.down.sql
│   ├── 002_create_orgs.up.sql
│   ├── 002_create_orgs.down.sql
│   ├── 003_create_api_keys.up.sql
│   ├── 003_create_api_keys.down.sql
│   ├── 004_create_subscriptions.up.sql
│   ├── 004_create_subscriptions.down.sql
│   ├── 005_create_usage_records.up.sql
│   ├── 005_create_usage_records.down.sql
│   └── 006_create_audit_log.up.sql
├── docs/
│   ├── ARCHITECTURE.md
│   ├── API.md
│   └── TIER_SYSTEM.md
├── .github/
│   └── workflows/
│       ├── ci.yml               # Build + test on PR
│       ├── deploy-staging.yml   # Deploy to staging on merge to main
│       └── deploy-prod.yml      # Deploy to production (manual trigger)
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

## Constitution Alignment

- **Privacy-First:** The cloud platform is strictly opt-in. Free-tier users have zero cloud interaction. Pro/Team users explicitly enable sync in their `config.toml`. Per-tenant Postgres schemas ensure data isolation. No cross-tenant data sharing without explicit opt-in to aggregate models. Data retention is configurable with automatic purging.

- **Daemon-First:** The daemon is the product; the cloud is an accelerator. sigild works fully offline on Free tier. Cloud ML and LLM responses use the same response format as local backends — sigild's code paths are identical regardless of where the computation happens. If the cloud is unreachable, sigild falls back to local gracefully.

- **Observable:** All cloud services expose `/healthz` and `/readyz` endpoints. Usage is metered and visible in the dashboard. The admin console provides audit logs. Prometheus metrics are exported for operational monitoring. The daemon reports cloud connection status via `sigilctl status`.

- **Minimal Dependencies:** Cloud services are Go binaries with Postgres and Redis — no exotic infrastructure. The dashboard extends the existing Preact fleet dashboard. Docker images are the deployment unit. Helm charts parameterize everything.

- **Progressive Trust:** The tier system embodies progressive trust. Free: trust nothing to the cloud. Pro: trust optional data and computation. Team: trust more data for team-level insights. Each step requires explicit user action (subscription, config change, org invitation acceptance).

## Relationship to Other Components

| Component | Relationship |
|-----------|-------------|
| `sigild` | Daemon-side sync agent sends events to ingest service. Cloud config structs (`cloud.api_key`, `cloud.sync_enabled`) live in sigild's `config.toml`. Inference engine routes to cloud ML/LLM proxy when configured. |
| `sigilctl` | New `sigilctl cloud status` command shows cloud connection health, sync state, usage. Lives in sigil repo. |
| `sigil-ml` | Cloud ML proxy manages hosted sigil-ml instances. The proxy presents the same HTTP API as the local sidecar. |
| `fleet/service/` | Extracted from sigil repo into `sigil-cloud/services/fleet/`. Fleet reporters in sigild continue to report to the cloud-hosted fleet service. |
| `fleet/dashboard/` | Extracted from sigil repo into `sigil-cloud/dashboard/`. Extended with personal analytics, billing, settings, team views. |
| `cloud/ingest/` | Extracted from sigil repo into `sigil-cloud/services/ingest/`. No functional changes, just new home. |
| `cloud/llm-proxy/` | Extracted from sigil repo into `sigil-cloud/services/llm-proxy/`. Extended with metering and tier enforcement. |
| VS Code / JetBrains extensions | Not directly affected. Cloud features are daemon-mediated — extensions talk to sigild, sigild talks to cloud. |
| Spec 016 (Tray App) | Dashboard onboarding complements tray app setup. Cloud status visible in tray app via sigild socket. |
| Spec 018 (Installers) | Installers bundle the daemon only. Cloud is configured post-install via `sigild init` or dashboard onboarding. |

## API Surface

### Auth Service (`/api/v1/auth/`)

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| POST | `/register` | Create account (email/password) | None |
| POST | `/login` | Login, receive JWT | None |
| GET | `/oauth/{provider}/callback` | OAuth callback (GitHub, Google) | None |
| POST | `/keys` | Generate API key | JWT |
| GET | `/keys` | List API keys (prefix only) | JWT |
| DELETE | `/keys/{id}` | Revoke API key | JWT |
| POST | `/keys/{id}/rotate` | Rotate API key (returns new key, old valid for grace period) | JWT |
| GET | `/me` | Current user profile + tier | JWT |
| POST | `/orgs` | Create org (Team tier) | JWT |
| POST | `/orgs/{id}/invite` | Invite member | JWT (owner/admin) |
| GET | `/orgs/{id}/members` | List org members | JWT (member+) |
| DELETE | `/orgs/{id}/members/{uid}` | Remove member | JWT (owner/admin) |

### Billing Service (`/api/v1/billing/`)

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| POST | `/checkout` | Create Stripe checkout session | JWT |
| GET | `/subscription` | Current subscription details | JWT |
| POST | `/portal` | Create Stripe customer portal session | JWT |
| POST | `/webhook` | Stripe webhook handler | Stripe signature |

### Ingest Service (`/api/v1/ingest/`)

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| POST | `/events` | Batch event upload | API key |
| POST | `/suggestions` | Batch suggestion upload | API key |
| GET | `/status` | Sync status for current user | API key |

### Fleet Service (`/api/v1/fleet/`)

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| POST | `/report` | Submit fleet metrics | API key |
| GET | `/metrics/{view}` | Query metrics (adoption, velocity, etc.) | JWT |
| GET | `/policy` | Get routing policy | API key |

### ML Proxy (`/api/v1/ml/`)

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| POST | `/predict` | Get ML prediction | API key |
| GET | `/models` | List available models for user | JWT |
| GET | `/status` | ML service health + model cache stats | JWT |

### LLM Proxy (`/api/v1/llm/`)

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| POST | `/complete` | LLM completion request | API key |
| POST | `/complete/stream` | Streaming LLM completion | API key |
| GET | `/usage` | Token usage for current billing period | JWT |

### Dashboard (`/api/v1/dashboard/`)

| Method | Path | Description | Auth |
|--------|------|-------------|------|
| GET | `/analytics/personal` | Personal suggestion stats + trends | JWT |
| GET | `/analytics/team` | Team aggregate stats (Team tier) | JWT (member+) |
| GET | `/usage` | Combined ML + LLM usage | JWT |

## Daemon-Side Config

The following configuration lives in sigild's `config.toml` (in the sigil repo, not sigil-cloud):

```toml
[cloud]
enabled = false                          # master switch
api_key = ""                             # from dashboard onboarding
endpoint = "https://api.sigilos.io"      # cloud API base URL

[cloud.sync]
enabled = false                          # opt-in data sync
events = true                            # sync events
suggestions = true                       # sync suggestions
interval = "5m"                          # sync batch interval

[cloud.ml]
enabled = false                          # use cloud ML instead of local sigil-ml
fallback_to_local = true                 # if cloud unreachable, use local

[cloud.llm]
enabled = false                          # use cloud LLM instead of local/BYOK
fallback_to_local = true                 # if cloud unreachable, use local
```

## Security Considerations

| Concern | Mitigation |
|---------|-----------|
| API key compromise | Keys are hashed (argon2) in DB. Rotation with grace period. Prefix-only display in dashboard. Audit log on key events. |
| Data in transit | TLS everywhere. Ingress terminates TLS with cert-manager auto-renewal. |
| Data at rest | Postgres encryption at rest (RDS default). S3 server-side encryption for model artifacts. |
| Tenant isolation | Per-tenant Postgres schemas. All queries scoped to authenticated tenant. No cross-tenant joins. |
| Rate limiting abuse | Redis-backed sliding window rate limiter at gateway. Per-tier limits. IP-based fallback for unauthenticated endpoints. |
| Stripe webhook spoofing | Webhook signature verification using `STRIPE_WEBHOOK_SECRET`. |
| Admin access | Admin console requires separate admin role. Impersonation is read-only. All admin actions audited. |
