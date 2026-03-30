# 019 — Sigil Cloud Platform: Implementation Plan

**Spec:** `specs/019-sigil-cloud/spec.md`
**Repo:** `sigil-tech/sigil-cloud` (new)
**Branch:** `main` (new repo)

---

## Pre-Implementation Gates

### DAG Gate
The cloud platform lives in a separate repository (`sigil-tech/sigil-cloud`). It does not import any Go packages from the `sigil` monorepo. Communication between sigild and cloud services is over HTTPS — no shared Go interfaces, no shared binaries. The only contract is the wire protocol (JSON over HTTP). **Pass.**

### Interface Gate
The cloud ML proxy MUST return responses in the same format as the local sigil-ml sidecar. The cloud LLM proxy MUST return responses in the same format as the local inference engine. This ensures sigild's code paths are identical regardless of backend. No new interfaces in the sigil repo — the existing `inference.Backend` interface already supports pluggable backends. **Pass.**

### Privacy Gate
Cloud features are strictly opt-in. Free-tier users have zero cloud interaction. Pro/Team users must explicitly set `cloud.enabled = true` and `cloud.sync.enabled = true` in their `config.toml`. Per-tenant Postgres schemas ensure data isolation. No cross-tenant data access. Configurable retention with automatic purging. API keys hashed with argon2. **Pass.**

### Simplicity Gate
Six Go services (auth, gateway, ingest, fleet, ml-proxy, llm-proxy) + one Preact dashboard. Two of the services (ingest, fleet) and the dashboard base already exist. Standard infrastructure: Postgres, Redis, S3, Kubernetes. No exotic dependencies. **Pass.**

---

## Technical Design

### What Already Exists

- **`fleet/service/`** (sigil repo) — Go HTTP service for anonymized fleet metrics. Postgres storage, JWT auth, OIDC. Serves 7 dashboard views. Will be extracted as-is into `sigil-cloud/services/fleet/`.
- **`fleet/dashboard/`** (sigil repo) — Preact dashboard with 7 metric views (adoption, velocity, cost, compliance, tasks, quality, ML). Will be extracted and extended into `sigil-cloud/dashboard/`.
- **`fleet/helm/`** (sigil repo) — Helm chart for the fleet service. Basis for the cloud Helm charts.
- **`cloud/ingest/`** (sigil repo) — Event ingest service receiving sync agent data into per-tenant Postgres. Will be extracted as-is into `sigil-cloud/services/ingest/`.
- **`cloud/llm-proxy/`** (sigil repo) — LLM proxy routing requests to providers. Will be extracted and extended with metering into `sigil-cloud/services/llm-proxy/`.

### What's New

1. **`sigil-tech/sigil-cloud` repository** — New repo with its own `go.mod`, CI, and deployment infrastructure
2. **Auth & billing service** — User registration, login, OAuth, Stripe, JWT, org management
3. **API gateway** — Routing, JWT validation, rate limiting, tier enforcement
4. **Cloud ML proxy** — Prediction routing to hosted sigil-ml, model caching, tenant-aware model selection
5. **User dashboard extensions** — Personal analytics, billing, settings, onboarding, team views, admin console
6. **Database migrations** — Shared schema for auth/billing/audit, per-tenant schemas for synced data
7. **Helm charts** — Per-service charts + umbrella chart for full platform deployment
8. **CI/CD pipelines** — Build, test, deploy to staging and production
9. **Terraform** — AWS infrastructure provisioning (EKS, RDS, ElastiCache, S3)

### Service Architecture

```
                        ┌─────────────┐
                        │   Ingress   │
                        │  (TLS/nginx)│
                        └──────┬──────┘
                               │
                        ┌──────▼──────┐
                        │   Gateway   │
                        │ JWT + Rate  │
                        │  Limiting   │
                        └──────┬──────┘
               ┌───────────────┼───────────────┐
               │               │               │
        ┌──────▼──────┐ ┌─────▼──────┐ ┌──────▼──────┐
        │    Auth     │ │   Ingest   │ │    Fleet    │
        │  + Billing  │ │  Service   │ │  Service    │
        └──────┬──────┘ └─────┬──────┘ └──────┬──────┘
               │               │               │
               │        ┌─────▼──────┐         │
               │        │  ML Proxy  │         │
               │        └─────┬──────┘         │
               │               │               │
               │        ┌─────▼──────┐         │
               │        │ LLM Proxy  │         │
               │        └─────┬──────┘         │
               │               │               │
        ┌──────▼───────────────▼───────────────▼──────┐
        │                  Postgres                    │
        │  sigil_cloud schema │ tenant_{id} schemas    │
        └─────────────────────────────────────────────┘
               │               │
        ┌──────▼──────┐ ┌─────▼──────┐
        │    Redis    │ │     S3     │
        │ rate limits │ │   models   │
        │   + cache   │ │            │
        └─────────────┘ └────────────┘
```

### Auth & Billing Service

The auth service handles user identity, API key lifecycle, Stripe billing, and org management. It is the only service that writes to the `users`, `orgs`, `api_keys`, and `subscriptions` tables.

**JWT Claims:**

```json
{
  "sub": "user_abc123",
  "email": "user@example.com",
  "tier": "pro",
  "org_id": "org_xyz789",
  "org_role": "admin",
  "iat": 1711800000,
  "exp": 1711886400
}
```

JWTs are signed with RS256 (asymmetric). The gateway and all services validate JWTs using the public key. Only the auth service holds the private key.

**Stripe Integration:**

- Checkout: auth service creates a Stripe Checkout Session. Frontend redirects to Stripe-hosted page. On success, Stripe webhook fires `checkout.session.completed`, auth service creates the subscription record and updates the user's tier.
- Billing portal: auth service creates a Stripe Customer Portal Session for plan changes, payment method updates, and cancellation.
- Metering: usage records are pushed to Stripe via the Metered Billing API at the end of each billing period (LLM tokens, ML predictions).

**OAuth Flow:**

1. Frontend redirects to `/api/v1/auth/oauth/github` (or `/google`)
2. Auth service redirects to provider's authorization URL with state parameter
3. Provider redirects back to `/api/v1/auth/oauth/{provider}/callback`
4. Auth service exchanges code for access token, fetches user profile
5. Creates or links user account, issues JWT

**API Key Lifecycle:**

- Generate: create a 32-byte random key, return the raw key once, store the argon2 hash + 8-char prefix in DB
- Authenticate: gateway receives `Authorization: Bearer sk_...` header, hashes the key, looks up by hash
- Rotate: generate new key, mark old key's `grace_expires_at` to now + 24h, return new key
- Revoke: set `revoked_at`, immediately invalid

### API Gateway

The gateway is a reverse proxy built with Go's `net/http/httputil.ReverseProxy`. It runs three middleware layers in order:

1. **TLS termination** — handled by ingress, not the gateway itself
2. **JWT/API key validation** — extracts token from `Authorization` header, validates signature, extracts claims. API keys are validated by hashing and checking against the auth service (with a short TTL cache in Redis to avoid per-request DB hits)
3. **Rate limiting** — Redis-backed sliding window counter keyed by user ID. Limits: Pro 1000/hr, Team 10000/hr
4. **Tier enforcement** — checks the `tier` claim against the requested path. Blocks Free users from `/api/v1/ml/*` and `/api/v1/llm/*`
5. **Header injection** — adds `X-Sigil-User-ID`, `X-Sigil-Org-ID`, `X-Sigil-Tier` headers to forwarded requests

**Routing table:**

| Path prefix | Backend service |
|---|---|
| `/api/v1/auth/` | auth:8080 |
| `/api/v1/billing/` | auth:8080 |
| `/api/v1/ingest/` | ingest:8080 |
| `/api/v1/fleet/` | fleet:8080 |
| `/api/v1/ml/` | ml-proxy:8080 |
| `/api/v1/llm/` | llm-proxy:8080 |
| `/api/v1/dashboard/` | dashboard:8080 |
| `/*` | dashboard:80 (static frontend) |

### Cloud ML Proxy

The ML proxy manages hosted sigil-ml instances and routes prediction requests.

**Model lifecycle:**

1. When a Pro/Team user first requests a prediction, the proxy checks if a personal model exists in S3 (`models/{user_id}/latest.pt`)
2. If no model exists, the proxy returns predictions from the aggregate model (Team) or a baseline model (Pro)
3. When enough synced data accumulates (configured threshold, e.g., 1000 events), the proxy triggers a model training job
4. Trained models are stored in S3 and the cache is invalidated

**Caching strategy:**

- Redis stores model metadata (user_id -> model version, S3 path, last accessed)
- Hot models (accessed in the last hour) are kept loaded in memory on the proxy instance
- Cold models are evicted from memory but remain in S3
- LRU eviction when memory cache exceeds configured limit (default 2GB)

**Response format compatibility:**

The proxy returns responses in the exact same JSON format as the local sigil-ml sidecar's `/predict` endpoint. sigild's inference engine does not know or care whether the prediction came from a local sidecar or the cloud proxy.

### Cloud LLM Proxy

The LLM proxy routes completion requests to Anthropic or OpenAI.

**Provider routing:**

1. Check if the user has a personal API key configured → use their key
2. Otherwise, use the platform's pooled key for the configured primary provider
3. If the primary provider returns a 5xx or times out, failover to the secondary provider

**Usage metering:**

- Every completion response includes token counts (prompt + completion)
- The proxy writes a usage record to Postgres (`usage_records` table)
- A background goroutine aggregates usage per user per billing period and pushes to Stripe metered billing

**Token budget enforcement:**

- Before sending to the provider, the proxy checks the user's remaining budget for the current period
- If the request would exceed the budget, return HTTP 429 with `Retry-After` set to the next billing period start
- Budget: Pro 500K tokens/month, Team 2M tokens/seat/month

### Dashboard

The dashboard extends the existing fleet dashboard (Preact + Vite) with new pages.

**Existing pages (from fleet/dashboard/):**
- Adoption metrics
- Velocity metrics
- Cost metrics
- Compliance metrics
- Task metrics
- Quality metrics
- ML metrics

**New pages:**
- Onboarding (guided cloud setup)
- Personal analytics (suggestion stats, trends)
- Cloud status (ML/LLM usage, sync status)
- Billing (plan, usage, upgrade)
- Settings (API keys, sync preferences, data retention)
- Team dashboard (Team tier only)
- Admin console (admin role only)

**Authentication:**

The dashboard uses the auth service for login. After login, the JWT is stored in an HttpOnly cookie. The dashboard's API client includes the JWT on all requests. Protected pages check the JWT claims for tier and role.

### Database Migrations

Managed by `golang-migrate`. Migrations are SQL files in `migrations/` with `up` and `down` variants. Applied automatically during deployment via an init container in the Helm chart.

**Tenant schema creation:**

When a new user enables sync for the first time, the ingest service creates a new Postgres schema (`tenant_{user_id}`) with the event/suggestion/task_session tables. This is a one-time operation per user, triggered by the first event batch.

### Local Development

`docker-compose.yml` provides the full stack locally:

```yaml
services:
  postgres:
    image: postgres:16
    environment:
      POSTGRES_DB: sigil_cloud
      POSTGRES_PASSWORD: dev
    ports: ["5432:5432"]

  redis:
    image: redis:7-alpine
    ports: ["6379:6379"]

  minio:
    image: minio/minio
    command: server /data
    ports: ["9000:9000"]

  auth:
    build: services/auth/
    environment:
      DATABASE_URL: postgres://postgres:dev@postgres:5432/sigil_cloud
      JWT_PRIVATE_KEY_PATH: /keys/private.pem
      STRIPE_SECRET_KEY: sk_test_...
    ports: ["8081:8080"]

  gateway:
    build: services/gateway/
    environment:
      JWT_PUBLIC_KEY_PATH: /keys/public.pem
      REDIS_URL: redis://redis:6379
      AUTH_URL: http://auth:8080
      INGEST_URL: http://ingest:8080
      FLEET_URL: http://fleet:8080
      ML_PROXY_URL: http://ml-proxy:8080
      LLM_PROXY_URL: http://llm-proxy:8080
    ports: ["8080:8080"]

  ingest:
    build: services/ingest/
    environment:
      DATABASE_URL: postgres://postgres:dev@postgres:5432/sigil_cloud
    ports: ["8082:8080"]

  fleet:
    build: services/fleet/
    environment:
      DATABASE_URL: postgres://postgres:dev@postgres:5432/sigil_cloud
    ports: ["8083:8080"]

  ml-proxy:
    build: services/ml-proxy/
    environment:
      DATABASE_URL: postgres://postgres:dev@postgres:5432/sigil_cloud
      REDIS_URL: redis://redis:6379
      S3_ENDPOINT: http://minio:9000
      S3_BUCKET: sigil-models
    ports: ["8084:8080"]

  llm-proxy:
    build: services/llm-proxy/
    environment:
      DATABASE_URL: postgres://postgres:dev@postgres:5432/sigil_cloud
      ANTHROPIC_API_KEY: sk-ant-...
    ports: ["8085:8080"]

  dashboard:
    build: dashboard/
    ports: ["3000:80"]
```

Run: `docker compose up` — full platform at `http://localhost:8080`.

---

## Implementation Phases

### Phase 0: Repo Scaffolding + Service Extraction

Create the `sigil-tech/sigil-cloud` repository. Extract existing services from the sigil monorepo. Establish the Go module, CI skeleton, and local dev environment.

**Tasks:**

1. Create `sigil-tech/sigil-cloud` repo with `go.mod`, `Makefile`, `.gitignore`, `README.md`
2. Copy `fleet/service/` from sigil repo into `services/fleet/`, adapting import paths
3. Copy `fleet/dashboard/` from sigil repo into `dashboard/`, preserving all 7 views
4. Copy `cloud/ingest/` from sigil repo into `services/ingest/`, adapting import paths
5. Copy `cloud/llm-proxy/` from sigil repo into `services/llm-proxy/`, adapting import paths
6. Create Dockerfiles for all 4 extracted services
7. Create `deploy/docker/docker-compose.yml` with Postgres, Redis, MinIO, and the 4 services
8. Create `migrations/` directory with initial schema for fleet tables (ported from fleet service's embedded migrations)
9. Create `.github/workflows/ci.yml` — build all services, run `go vet`, run tests
10. Verify: `docker compose up` starts all services; fleet dashboard loads at `localhost:3000`

**Key decisions:**
- Go module path: `github.com/sigil-tech/sigil-cloud`
- Go version: match sigil repo (Go 1.24)
- Each service is a standalone `main.go` in its directory — no shared binary
- Extracted services keep their existing HTTP handlers and business logic; only import paths change

**What stays in sigil repo:**
- `fleet/helm/` and `fleet/nix/` — these deploy the fleet service when self-hosted
- Fleet reporter code in `internal/fleet/`
- Sync agent code in sigild
- Cloud config structs in `internal/config/`
- The originals in `fleet/service/`, `fleet/dashboard/`, `cloud/ingest/`, `cloud/llm-proxy/` — removed from sigil repo after extraction is validated

**Verification:** `docker compose up` brings up all 4 extracted services. Fleet dashboard renders at `localhost:3000`. Ingest service accepts a test event batch via curl. LLM proxy responds to a health check.

---

### Phase 1: Auth & Billing Service

Build the auth service from scratch. This is the foundation for all cloud features — no other service can enforce access control without it.

**Tasks:**

1. Create `services/auth/main.go` — HTTP server on port 8080
2. Implement `handler/register.go` — email/password registration with argon2 password hashing
3. Implement `handler/login.go` — email/password login, JWT issuance
4. Implement `handler/oauth.go` — GitHub and Google OAuth flows
5. Implement `jwt/jwt.go` — RS256 JWT signing and validation, key pair management
6. Implement `handler/keys.go` — API key generation, listing, rotation, revocation
7. Implement `stripe/checkout.go` — Stripe Checkout Session creation for Pro/Team upgrade
8. Implement `stripe/webhook.go` — Stripe webhook handler for subscription lifecycle events
9. Implement `stripe/portal.go` — Stripe Customer Portal Session creation
10. Implement `handler/orgs.go` — Org CRUD, member invitation, role management
11. Create migrations: `001_create_users`, `002_create_orgs`, `003_create_api_keys`, `004_create_subscriptions`, `005_create_usage_records`, `006_create_audit_log`
12. Add auth service to `docker-compose.yml`
13. Write unit tests for JWT signing/validation, API key hashing, tier enforcement

**Key decisions:**
- Password hashing: argon2id (memory-hard, resistant to GPU cracking)
- JWT signing: RS256 with 4096-bit RSA key pair. Private key in auth service only. Public key distributed to gateway and all services for validation.
- JWT expiry: 24 hours. Refresh tokens: 30 days (stored hashed in DB).
- API key format: `sk_live_` prefix + 32 random bytes base62-encoded. Stored as argon2 hash.
- Stripe products: two price IDs — one for Pro monthly, one for Team per-seat monthly.

**Verification:** Register a user, login, receive JWT. Generate API key. Create Stripe checkout session (test mode). Webhook fires, subscription created. JWT now includes `tier: "pro"`. Create org, invite member (via email stub).

---

### Phase 2: API Gateway with Tier Enforcement

Build the gateway that fronts all services. This replaces direct access to backend services and adds authentication, rate limiting, and tier enforcement.

**Tasks:**

1. Create `services/gateway/main.go` — HTTP server on port 8080
2. Implement `middleware/jwt.go` — JWT validation using the auth service's public key
3. Implement `middleware/apikey.go` — API key validation (hash + lookup, cached in Redis)
4. Implement `middleware/ratelimit.go` — Redis sliding window rate limiter per user ID
5. Implement `middleware/tier.go` — Tier enforcement (block Free from cloud ML/LLM)
6. Implement `proxy/router.go` — Path-prefix-based reverse proxy to backend services
7. Implement `middleware/headers.go` — Inject `X-Sigil-User-ID`, `X-Sigil-Org-ID`, `X-Sigil-Tier`
8. Add Prometheus metrics endpoint (`/metrics`)
9. Update `docker-compose.yml` — gateway is now the single entry point
10. Write integration tests: valid JWT routes correctly, invalid JWT returns 401, Free tier gets 403 on ML/LLM paths, rate limit kicks in at threshold

**Key decisions:**
- Built on Go `net/http/httputil.ReverseProxy` — no external gateway framework
- Rate limiter: Redis `EVALSHA` with a sliding window Lua script (standard pattern)
- API key cache: Redis with 5-minute TTL. Cache miss triggers a hash + DB lookup.
- Health checks (`/healthz`, `/readyz`) bypass auth

**Verification:** Start full stack. Login, get JWT. Hit `/api/v1/fleet/metrics/adoption` — routed to fleet service. Hit `/api/v1/ml/predict` as Free user — 403. Hit rate limit — 429 with `Retry-After`. Prometheus metrics show request counts.

---

### Phase 3: Dashboard Expansion (Personal Analytics, Settings)

Extend the extracted fleet dashboard with personal analytics, settings, and onboarding pages. This is the first user-facing cloud feature beyond the existing fleet views.

**Tasks:**

1. Add login page to dashboard — email/password form, OAuth buttons, JWT cookie storage
2. Create `pages/Onboarding.tsx` — step-by-step guide: create account, generate API key, show `config.toml` snippet, test connection
3. Create `pages/PersonalAnalytics.tsx` — suggestion acceptance rate (pie chart), productivity trend (line chart over days/weeks), task pattern breakdown (bar chart by category)
4. Create `pages/CloudStatus.tsx` — ML prediction count, LLM token usage vs budget, sync status (last sync time, events synced)
5. Create `pages/Billing.tsx` — current plan, usage meters, upgrade/downgrade buttons (Stripe portal redirect)
6. Create `pages/Settings.tsx` — API key list with rotate/revoke, sync toggle, data retention selector
7. Create `components/ApiKeyManager.tsx` — generate, display prefix, copy-to-clipboard, rotate, revoke
8. Create `components/TrendChart.tsx` — reusable line chart (lightweight, no heavy charting lib — use `<canvas>` or `uplot`)
9. Create `components/UsageBar.tsx` — progress bar showing usage vs limit
10. Create backend endpoint `GET /api/v1/dashboard/analytics/personal` — queries tenant schema for suggestion stats
11. Add dashboard service to gateway routing
12. Write component tests for key UI flows

**Key decisions:**
- No heavy charting library. Use `uplot` (~35KB) for line/bar charts, or raw `<canvas>` for simple visualizations.
- Onboarding generates a real API key inline — no separate step needed.
- Settings page talks directly to the auth service API for key management.

**Verification:** Login to dashboard. Complete onboarding flow — API key generated, config snippet shown. Personal analytics page shows suggestion data from synced events. Billing page shows current plan. Settings page allows API key rotation.

---

### Phase 4: ML Proxy Integration

Build the cloud ML proxy that routes prediction requests to hosted sigil-ml instances. This enables Pro/Team users to get ML predictions without installing the Python sidecar.

**Tasks:**

1. Create `services/ml-proxy/main.go` — HTTP server on port 8080
2. Implement `handler/predict.go` — accept prediction request, route to sigil-ml instance, return response
3. Implement `cache/cache.go` — Redis-backed model metadata cache + in-memory LRU for hot models
4. Implement `s3/loader.go` — load model artifacts from S3 on cache miss
5. Implement model selection logic: personal model if exists, fallback to aggregate (Team) or baseline (Pro)
6. Implement prediction latency tracking (Prometheus histogram)
7. Add Dockerfile and docker-compose entry
8. Add Helm chart for ml-proxy
9. Write unit tests for model selection logic, cache eviction, tier enforcement
10. Write integration test: send prediction request, verify response format matches sigil-ml local sidecar

**Key decisions:**
- The proxy does NOT run sigil-ml inline. It manages separate sigil-ml instances (containers) and forwards HTTP requests to them.
- Model loading: download from S3 to local disk, then signal the sigil-ml instance to load it. Model files are PyTorch checkpoints.
- Cache eviction: LRU by last-access time. Evicted models are unloaded from the sigil-ml instance but remain in S3.
- Baseline model: a pre-trained model on anonymized aggregate data, shipped with the platform. Available to all Pro/Team users.

**Verification:** Deploy ml-proxy + a sigil-ml instance. Send prediction request as Pro user with no personal model — baseline model responds. Upload a personal model to S3. Send prediction request — personal model responds. Check latency histogram in Prometheus.

---

### Phase 5: LLM Proxy Extraction

Extract the existing LLM proxy from the sigil repo, add metering and tier enforcement.

**Tasks:**

1. Finalize extraction of `cloud/llm-proxy/` into `services/llm-proxy/` (if not already complete from Phase 0)
2. Implement `metering/meter.go` — count prompt + completion tokens per request, write to `usage_records`
3. Implement token budget enforcement — check remaining budget before forwarding to provider
4. Implement `provider/anthropic.go` — Anthropic Claude API client
5. Implement `provider/openai.go` — OpenAI API client
6. Implement provider failover logic — primary timeout/5xx triggers failover to secondary
7. Implement streaming support (`/complete/stream` endpoint using SSE)
8. Implement per-user API key support — if user has their own provider key, use it instead of pooled
9. Add Dockerfile, docker-compose entry, Helm chart
10. Write unit tests for metering, budget enforcement, failover logic
11. Write integration test: send completion request, verify usage record created

**Key decisions:**
- Token counting: use tiktoken-compatible counting for OpenAI models. For Anthropic, count from the response's `usage` field.
- Budget period: calendar month, resetting on the 1st. Usage records aggregated per user per month.
- Failover: on 5xx or timeout (10s default), retry once with the secondary provider. If secondary also fails, return 503.
- Streaming: standard SSE (`text/event-stream`). Each chunk includes incremental token count. Final chunk includes total usage.

**Verification:** Send completion request. Response returned from Anthropic. Usage record shows correct token count. Approach budget limit — next request returns 429. Simulate Anthropic failure — failover to OpenAI succeeds. Stream a long response — chunks arrive incrementally.

---

### Phase 6: Team Features (Org Management, Aggregate Models)

Add Team-tier-only features: team dashboards, member management, and aggregate model training.

**Tasks:**

1. Create `pages/TeamDashboard.tsx` — team member list, aggregate acceptance rate, team productivity trends, top suggestion categories across team
2. Implement team member management in dashboard: invite (email), accept invitation, assign roles, remove
3. Create `GET /api/v1/dashboard/analytics/team` backend endpoint — queries across team members' tenant schemas (with permission checks)
4. Implement aggregate model training pipeline: collect anonymized events from opted-in team members, train sigil-ml model, store in S3 as `models/org_{org_id}/aggregate.pt`
5. Update ML proxy model selection: Team users with aggregate opt-in get predictions from the aggregate model in addition to personal
6. Implement org-level settings: allowed LLM models, custom rate limits, data retention policy
7. Add org-level usage rollup: total ML predictions, total LLM tokens across team
8. Write tests for team analytics queries, aggregate model selection, permission checks

**Key decisions:**
- Team analytics aggregate across opted-in members only. Members who disable sync are excluded.
- Aggregate model training runs as a batch job (Kubernetes CronJob), not inline.
- Aggregate training uses only anonymized data: event kinds, timestamps, task categories — never file paths, command text, or suggestion content.
- Role hierarchy: owner > admin > member. Owners can do everything. Admins can manage members and settings. Members can view team dashboard.

**Verification:** Create org as Team user. Invite two members. All three sync data. Team dashboard shows aggregate metrics across all three. Aggregate model trains successfully. ML proxy returns aggregate model predictions for Team user with opt-in.

---

### Phase 7: Admin Console

Build the admin console for platform operators (Sigil team internal use).

**Tasks:**

1. Create `pages/Admin.tsx` — admin-only page, requires `role: "admin"` in JWT
2. Implement org listing with search and filters (tier, member count, creation date)
3. Implement org detail view: members, subscription status, usage metrics
4. Implement user listing with search (email, tier, last active)
5. Implement audit log viewer: filterable by user, action, date range
6. Implement usage monitoring: per-org and per-user request counts, token usage, storage consumption
7. Implement policy management: configure allowed LLM models per org, set custom rate limits
8. Implement read-only impersonation: "view as user" shows the dashboard as that user would see it (no mutations)
9. Create `GET /api/v1/admin/*` backend endpoints with admin role enforcement
10. Write tests for admin role enforcement, audit log queries

**Key decisions:**
- Admin role is assigned via a database flag, not a separate auth system. Bootstrap: first user created with `ADMIN_EMAIL` env var gets admin.
- Impersonation: the admin console issues a read-only JWT with the target user's claims plus an `impersonator` claim. Backend services honor the user claims for data access but reject any mutation if `impersonator` is set.
- Audit log retention: 2 years, not configurable (compliance).

**Verification:** Login as admin. View org list. Click into an org — see members, usage. View audit log — see auth events from Phases 1-6. Impersonate a user — see their dashboard data read-only. Attempt mutation while impersonating — rejected.

---

### Phase 8: CI/CD, Monitoring, Alerting

Establish the production deployment pipeline and operational infrastructure.

**Tasks:**

1. Create `.github/workflows/ci.yml` — on PR: build all services, run `go vet`, run tests, lint
2. Create `.github/workflows/deploy-staging.yml` — on merge to main: build Docker images, push to ECR, deploy to staging EKS cluster via Helm
3. Create `.github/workflows/deploy-prod.yml` — manual trigger with approval: promote staging images to production
4. Create `deploy/terraform/main.tf` — EKS cluster, RDS Postgres, ElastiCache Redis, S3 bucket, IAM roles
5. Create `deploy/helm/sigil-cloud/` — umbrella Helm chart referencing per-service sub-charts
6. Create per-service Helm charts with `Deployment`, `Service`, `HPA`, `PDB`, health probes
7. Configure database migration init container in Helm charts (runs `golang-migrate` before service starts)
8. Set up Prometheus + Grafana for metrics collection and dashboarding
9. Set up alerting rules: service down (5 min), error rate > 5% (15 min), latency p99 > 2s (15 min), rate limit exhaustion > 50% of users (30 min)
10. Set up structured logging (JSON) with correlation IDs across services
11. Create runbook for common operational tasks: scale service, rotate JWT keys, database failover, incident response

**Key decisions:**
- Docker base image: `gcr.io/distroless/static-debian12` (minimal attack surface)
- Image tagging: `{service}:{git-sha}` for traceability, `{service}:{semver}` for releases
- Database migrations: `golang-migrate` CLI in an init container. Runs on every deployment. Idempotent.
- Staging cluster: single-node, same Helm charts as production but with lower resource limits
- Production cluster: multi-AZ EKS with autoscaling node groups
- Monitoring: Prometheus scrapes all services via ServiceMonitor CRDs. Grafana dashboards per service.

**Verification:** Merge to main triggers staging deployment. All services healthy in staging. Run smoke tests (register, login, sync events, get prediction, get completion). Manual approval promotes to production. Grafana dashboards show request metrics. Kill a service pod — alert fires within 5 minutes, pod auto-recovers.

---

## Testing Strategy

### Unit Tests (Go)

| Test | Package | What it verifies |
|------|---------|-----------------|
| `TestJWT_SignAndVerify` | `services/auth/jwt` | RS256 signing, validation, claim extraction |
| `TestJWT_Expired` | `services/auth/jwt` | Expired tokens rejected |
| `TestAPIKey_HashAndVerify` | `services/auth/handler` | argon2 hashing, verification |
| `TestAPIKey_Rotate` | `services/auth/handler` | Old key valid during grace period, new key works |
| `TestRateLimit_SlidingWindow` | `services/gateway/middleware` | Rate limit correctly applied, reset after window |
| `TestTierEnforcement_Free` | `services/gateway/middleware` | Free users blocked from ML/LLM paths |
| `TestTierEnforcement_Pro` | `services/gateway/middleware` | Pro users allowed ML/LLM, blocked from team endpoints |
| `TestIngest_Idempotent` | `services/ingest/handler` | Replayed batch does not create duplicates |
| `TestIngest_TenantIsolation` | `services/ingest/tenant` | Events stored in correct tenant schema |
| `TestMLProxy_ModelSelection` | `services/ml-proxy/handler` | Personal > aggregate > baseline fallback |
| `TestMLProxy_CacheEviction` | `services/ml-proxy/cache` | LRU eviction works, cold model reloads |
| `TestLLMProxy_Metering` | `services/llm-proxy/metering` | Token counts recorded correctly |
| `TestLLMProxy_BudgetEnforcement` | `services/llm-proxy/handler` | Over-budget requests rejected with 429 |
| `TestLLMProxy_Failover` | `services/llm-proxy/provider` | Primary failure triggers secondary |
| `TestStripe_WebhookVerification` | `services/auth/stripe` | Webhook signature validated |

Run with: `go test ./services/...`

### Frontend Tests

| Test | What it verifies |
|------|-----------------|
| `Onboarding.test.tsx` | Step flow completes, API key displayed, config snippet generated |
| `ApiKeyManager.test.tsx` | Generate, rotate, revoke flows update UI correctly |
| `PersonalAnalytics.test.tsx` | Charts render with mock data |
| `TeamDashboard.test.tsx` | Team metrics displayed, permission check for non-Team users |
| `Admin.test.tsx` | Admin-only routes blocked for non-admin JWT |

Run with: `cd dashboard && npm test`

### Integration Tests

Run against Docker Compose stack:

```bash
# Start full stack
docker compose up -d

# Wait for health checks
./scripts/wait-for-healthy.sh

# Run integration tests
go test ./tests/integration/... -tags=integration

# Tests cover:
# 1. Register → login → JWT contains correct claims
# 2. Generate API key → use it to authenticate ingest request
# 3. Ingest events → query personal analytics → data present
# 4. Upgrade to Pro → ML proxy accepts request
# 5. Create org → invite member → team dashboard shows aggregate
# 6. LLM completion → usage record → approaching budget → 429
# 7. Rate limit → 429 with Retry-After
# 8. Free user → ML proxy → 403
```

### Load Tests

| Test | Target | Tool |
|------|--------|------|
| Gateway throughput | 10K req/s sustained | k6 |
| Ingest batch latency | p99 < 200ms for 100-event batch | k6 |
| ML proxy latency | p99 < 500ms (hot model) | k6 |
| LLM proxy latency | p99 < 2s (completion) | k6 |
| Rate limiter accuracy | < 1% over-admission at 1000/hr | k6 |

### Performance Targets

| Metric | Target |
|--------|--------|
| Gateway p50 latency (passthrough) | < 5ms |
| Gateway p99 latency (passthrough) | < 20ms |
| Auth login latency | < 200ms |
| Ingest 100-event batch | < 200ms |
| ML proxy hot model prediction | < 500ms |
| ML proxy cold model prediction | < 5s (model load) |
| LLM proxy completion (non-streaming) | < 10s (depends on provider) |
| Dashboard page load | < 2s |
| Docker image size (per service) | < 30MB |
