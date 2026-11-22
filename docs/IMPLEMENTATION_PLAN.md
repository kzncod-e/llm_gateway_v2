# Implementation Plan

> **Status (2026-11-23):** all milestones M5–M12 implemented and verified; see `PROJECT_STATUS.md`. The dashboard source now lives in the sibling `llm_gateway_dashboard` repository. Deviations from this plan are recorded in `DECISIONS.md`.

Tasks are ordered by dependency. Each task lists goal, dependencies, modules, details, tests and Definition of Done (DoD). "Tests pass" always means `go test ./...` (backend) or `pnpm test` (frontend) green.

## M5 — Infrastructure foundation

### T5.1 Compose stack
- **Goal**: `docker compose up` starts postgres, redis, api, mock-llm, web, prometheus, grafana, jaeger.
- **Deps**: none. **Files**: `docker-compose.yml`, `service/Dockerfile`, `../llm_gateway_dashboard/Dockerfile`, `deploy/prometheus/prometheus.yml`, `deploy/grafana/**`, `.env.example`. The dashboard is maintained in its own sibling repository.
- **Details**: Postgres on host port 55432 (host already uses 5432), Redis 56379, API 8080, web 3000, Grafana 3001, Prometheus 9090, Jaeger UI 16686. Health checks on postgres/redis; api waits for both.
- **Tests**: `docker compose up -d` then `curl /readyz`, Prometheus target up.
- **DoD**: all services healthy.

## M6 — Backend core

| Task | Goal | Modules | Tests | DoD |
|---|---|---|---|---|
| T6.1 Bootstrap | `cmd/api` with `serve`/`migrate` subcommands, graceful shutdown | `cmd/api` | build | starts, stops on SIGTERM draining |
| T6.2 Config | env parsing + validation, defaults | `internal/config` | unit: required vars, defaults, invalid values | clear error messages on bad config |
| T6.3 Database | pgx pool with tracer | `internal/database` | integration ping | pool connects |
| T6.4 Migrations | embedded runner + `0001_init.sql` | `internal/database` | integration: apply twice is idempotent | schema created |
| T6.5 Logging | slog JSON, request logger middleware | `internal/observability` | unit: no secrets in log line | access log line per request |
| T6.6 Health | `/healthz`, `/readyz` | `handler` | httptest | readyz reflects pg/redis |
| T6.7 Auth | users, sessions, login/logout/me, admin bootstrap, RBAC middleware | `security`, `repository`, `service`, `handler/controlplane` | unit: password, session hashing; API: login success/failure, protected route 401 | console can log in |
| T6.8 Teams | CRUD, members, limits, budget config | same | API tests incl. member isolation | non-members get 404 |
| T6.9 API keys | create/list/revoke, auth middleware + cache | same | create→auth works, revoke→401, raw key never listed | key shown once |
| T6.10 Provider registry | providers, credentials (encrypted), models, pricing, routing policies, catalog snapshot | same + `gateway/catalog` | unit: AES round trip, tamper detection; API: credentials never returned | catalog reloads after mutation |

## M7 — Gateway engine

| Task | Goal | Modules | Tests |
|---|---|---|---|
| T7.1 Unified contract | request/response/chunk types, validation | `provider/types.go`, `handler/dataplane` | unit validation table |
| T7.2 Adapter interface + error taxonomy | `Adapter`, `Stream`, `Error`, `IsRetryable` | `provider` | unit |
| T7.3 OpenAI | request mapping, response, SSE parse, usage | `provider/openai` | httptest fixtures for success, errors, stream with usage |
| T7.4 Anthropic | system extraction, role merging, event stream | `provider/anthropic` | same + 529 |
| T7.5 Gemini | contents/parts mapping, SSE, usageMetadata | `provider/gemini` | same |
| T7.6 Routing | alias → explicit → model-based | `gateway/router.go` | unit table |
| T7.7 Retry | backoff with jitter, Retry-After, caps | `gateway/retry.go` | unit with fake sleeper |
| T7.8 Fallback | target loop | `gateway/engine.go` | engine tests with fake adapters |
| T7.9 Breaker | state machine | `breaker` | unit: transitions, concurrent Allow, half-open probes, `-race` |
| T7.10 Non-stream response | JSON writer + headers | `handler/dataplane` | API test end-to-end with httptest upstream |
| T7.11 SSE | commit-on-first-chunk, error event, disconnect | `gateway/stream.go` | stream tests: success, failover before first chunk, mid-stream drop, client cancel |

**DoD M7**: all of the above with mocked providers; no real keys needed.

## M8 — Usage & cost

| Task | Goal | Modules | Tests |
|---|---|---|---|
| T8.1 Usage normalization | per-provider usage → `provider.Usage` | adapters | fixture tests incl. cached tokens |
| T8.2 Pricing | effective-dated lookup, `Cost()` | `pricing` | unit: rounding, overflow, categories |
| T8.3 Recorder | settlement transaction | `usage`, `repository` | integration: rows written, reservation settled |
| T8.4 Budget | reserve/settle/release/sweep | `budget` | integration: **100 concurrent reservations never exceed limit** |
| T8.5 Rate limit | RPM + concurrency Lua | `ratelimit` | miniredis: limit enforced, concurrent acquires, lease expiry |

## M9 — Frontend
Order: tokens/theme → layout (sidebar/header) → common components (stat card, data table, states) → auth → overview → providers → teams → usage → request logs → playground → reliability → settings. Each feature: `api.ts`, `hooks.ts`, components, page. Tests: Vitest for formatters, schemas and key components; Playwright for login → create team → create key → playground stream.

## M10 — Observability
Metrics registry and middleware, provider metrics in engine, breaker gauge, OTel provider + spans, pgx tracer, redisotel, Grafana dashboard JSON. Verify by sending traffic and checking Prometheus queries and Jaeger traces.

## M11 — Hardening
Deliberate failure tests (see PRD §8 and SYSTEM_ARCHITECTURE §7): provider timeout, 429, 503, Redis down, Postgres down, client disconnect, provider disconnect mid-stream, unknown usage, budget exhausted, concurrent requests. `go test -race ./...`, `go vet`, frontend lint/typecheck/tests, e2e.

## M12 — Documentation
README, API_REFERENCE, DATABASE_SCHEMA, DEVELOPMENT_GUIDE, DEPLOYMENT_GUIDE; refresh the planning docs to match the code.
