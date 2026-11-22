# Development Guide

## Prerequisites

| Tool | Version used | For |
|---|---|---|
| Go | 1.27 | `service/` |
| Node.js | 24+ (26 tested) | `../llm_gateway_dashboard/` |
| pnpm | 11 | `../llm_gateway_dashboard/` |
| Docker + Compose v2 | any recent | Postgres, Redis, monitoring, full stack |
| PostgreSQL | 16+ (optional) | Running integration tests without Docker |

## Running the pieces locally

Start infrastructure only, then run the gateway from source in one terminal:

```sh
./scripts/gen-env.sh                       # once
docker compose up -d postgres redis mock-llm prometheus grafana jaeger

cd service
set -a; . ../.env; set +a                  # load secrets
export DATABASE_URL="postgres://gateway:${POSTGRES_PASSWORD}@localhost:55432/gateway?sslmode=disable"
export REDIS_URL=redis://localhost:56379/0 MOCK_PROVIDER_URL=http://localhost:8090 OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4318
go run ./cmd/api

```

In a second terminal, run the dashboard from its sibling repository:

```sh
cd ../llm_gateway_dashboard
pnpm install
BACKEND_URL=http://localhost:8080 pnpm dev   # http://localhost:3000
```

Compose publishes Postgres on **55432** and Redis on **56379** to avoid clashing with local installations. Jaeger's OTLP port (4318) is only exposed inside the Compose network; add `"127.0.0.1:4318:4318"` under `jaeger.ports` if you want traces from a gateway running on the host.

Run `go run ./cmd/mockprovider` instead of the container if you prefer (`MOCK_ADDR`, `MOCK_CHUNK_DELAY`).

## Configuration

All configuration is environment variables, documented in [`.env.example`](../.env.example). The gateway refuses to start when a required value is missing or invalid (`DATABASE_URL`, a 32-byte base64 `ENCRYPTION_KEY`, an `API_KEY_PEPPER` of at least 32 characters, and `ADMIN_PASSWORD` of at least 12 characters when `ADMIN_EMAIL` is set).

| Variable | Default | Purpose |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Listen address |
| `DATABASE_URL` | required | Postgres DSN |
| `REDIS_URL` | `redis://localhost:6379/0` | Rate limiting |
| `ENCRYPTION_KEY` | required | AES-256 key for provider credentials |
| `API_KEY_PEPPER` | required | HMAC key for gateway API key hashes |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | empty | First admin, created only if no users exist |
| `SEED_DEMO`, `DEMO_API_KEY`, `MOCK_PROVIDER_URL` | `false`, empty, `http://localhost:8090` | Demo data against the mock provider |
| `AUTO_MIGRATE` | `true` | Apply migrations on startup |
| `COOKIE_SECURE` | `false` | Set `true` behind HTTPS |
| `TRUSTED_PROXIES` | empty | CIDRs allowed to set `X-Forwarded-For` |
| `RATE_LIMIT_FAIL_OPEN` | `false` | Behaviour when Redis is down |
| `DEFAULT_TEAM_RPM`, `DEFAULT_TEAM_CONCURRENCY` | 600, 50 | Limits for teams without their own |
| `DEFAULT_MAX_OUTPUT_TOKENS` | 4096 | Applied when clients omit `max_tokens`; bounds reservations |
| `REQUEST_TIMEOUT`, `STREAM_TIMEOUT` | 120s, 10m | Overall request deadlines |
| `RESERVATION_TTL` | 15m | Must exceed `STREAM_TIMEOUT` |
| `BREAKER_FAILURE_THRESHOLD`, `BREAKER_OPEN_DURATION` | 5, 30s | Circuit breaker |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME` | empty, `llm-gateway` | Tracing (`host:port`, OTLP/HTTP); empty disables |

Dashboard: `BACKEND_URL` (where the `/api` proxy forwards; read at runtime) and `PORT`. The dashboard source is in the sibling repository `../llm_gateway_dashboard`.

## Code map

### Backend (`service/`)

| Package | Responsibility |
|---|---|
| `cmd/api` | `serve`, `migrate`, `healthcheck` subcommands; graceful shutdown |
| `cmd/mockprovider` | Serves `internal/mockllm` |
| `internal/app` | Wires dependencies; shared by `main` and end-to-end tests |
| `internal/config` | Environment parsing and validation |
| `internal/database` | pgx pool and embedded migration runner |
| `internal/provider` | Adapter interface, normalized types, error taxonomy, SSE reader; `openai/`, `anthropic/`, `gemini/` adapters |
| `internal/gateway` | Engine: rate limit → route → reserve → attempts (retry, fallback, breaker) → JSON or SSE → record |
| `internal/breaker` | Circuit breaker and per-provider registry |
| `internal/ratelimit` | Redis Lua sliding window and concurrency leases |
| `internal/budget` | Postgres reservation ledger and sweeper |
| `internal/pricing` | Price versions and cost arithmetic |
| `internal/usage` | Transactional recording of requests, attempts, usage and settlement |
| `internal/repository` | All control-plane SQL |
| `internal/service` | Control-plane use cases: validation, authorization, audit |
| `internal/handler`, `internal/router` | HTTP handlers and route/middleware wiring |
| `internal/observability` | Metrics, tracing, access log, security middleware |
| `internal/security` | AES-GCM secret box, key hashing, passwords, session tokens |
| `internal/seed` | Demo data |
| `internal/mockllm` | Mock OpenAI/Anthropic/Gemini wire formats with failure injection |

### Dashboard frontend (`../llm_gateway_dashboard/src/`)

| Path | Responsibility |
|---|---|
| `app/(console)/*/page.tsx` | Thin route files (metadata + feature view). Dynamic segments await `params` inside `<Suspense>` |
| `app/(auth)/login` | Login page |
| `app/api/[...path]/route.ts` | Same-origin streaming proxy to the control plane (header allowlist, fixed origin) |
| `proxy.ts` | Redirects visitors without a session cookie to `/login` (Next 16's replacement for middleware) |
| `features/<area>/api.ts` | TanStack Query hooks per area |
| `features/<area>/*-view.tsx` | Page views and feature components |
| `components/common` | Data table, charts, stat cards, states, badges, form field |
| `components/ui` | shadcn/ui primitives (Radix) themed by `globals.css` |
| `lib` | API client, types, formatters |

The console is dark-only ("Charcoal Sand"); the palette and status colors are CSS variables in `app/globals.css`.

> **Next.js 16 note.** `../llm_gateway_dashboard/AGENTS.md` points to the bundled docs in that repository's `node_modules/next/dist/docs/`. With Cache Components enabled, anything that reads the session or the current time must render inside `<Suspense>`; the dashboard layout provides one boundary around every page.

## Tests

| Suite | Command | Needs |
|---|---|---|
| Go unit tests | `cd service && go test -race ./...` | nothing |
| Go integration + end-to-end | `TEST_DATABASE_URL=postgres://…/llm_gateway_test?sslmode=disable go test -race ./...` | An empty Postgres database. Each test creates and drops its own schema |
| Dashboard lint, types, unit | `cd ../llm_gateway_dashboard && pnpm lint && pnpm typecheck && pnpm test` | nothing |
| Browser end-to-end | `cd ../llm_gateway_dashboard && pnpm build && E2E_PASSWORD=… pnpm test:e2e` | Gateway on :8080 with `SEED_DEMO=true`; Playwright starts the built dashboard on :3100. Set `E2E_BASE_URL=http://localhost:3000` to test the Compose dashboard instead |

What is covered:

- **Adapters:** request mapping, response and usage normalization (cached, cache-write and reasoning tokens), error classification for 400/401/404/429/5xx/529, timeouts and unreachable hosts, SSE parsing, and truncated streams.
- **Engine:** retry then fallback, non-retryable stop, `Retry-After`, circuit skipping, timeouts charged at the estimate, budget and pricing rejections, rate limiting, streaming success, failover before the first chunk, interruption after it, and client disconnect.
- **Budget:** 100 concurrent reservations against real Postgres (exactly 10 of 10% each succeed), settle, double-settle, release, and expiry sweep.
- **Rate limit:** concurrent RPM (exactly N pass), sliding window, leases and expiry, Redis down (fail closed or open).
- **End-to-end API (real router, Postgres, miniredis, mock provider):** auth, CSRF guard, all providers, fallback, streaming, interrupted streams, logs and attempts, analytics, rate limit, budget exhaustion, key revocation, team isolation, circuit breaker, metrics hygiene, login throttling, and absence of secrets and prompts from logs.
- **Frontend:** formatters, API client, SSE parser, login validation and open-redirect guard, data-table states, and a Playwright flow (login → team → key → gateway call → playground stream → logs with fallback attempts).

## Adding a provider

1. Create `service/internal/provider/<name>/` implementing `provider.Adapter` (`ChatCompletion`, `StreamChatCompletion`, `Type`). Return only `*provider.Error` via `provider.PostJSON`, `FromStatus` and `FromTransport`, and normalize usage into the disjoint categories of `provider.Usage`.
2. Add fixture tests with `httptest` like the existing adapters.
3. Register it in `internal/app/app.go` (`Adapters`), add the type to `model.ProviderTypes`, the `providers.type` CHECK constraint (new migration) and the console's type select.

Nothing in the engine changes.

## Adding a migration

Create `service/internal/database/migrations/0002_<name>.sql`. It is applied on next start. Run the integration tests to verify.

## Mock provider failure injection

| Trigger | Effect |
|---|---|
| Model name contains `fail-503` / `429` / `slow` / `drop-stream` | 503 / 429 with `Retry-After: 1` / 5 s delay / stream ends after a few chunks without an end marker |
| `curl -d '{"openai":"503"}' localhost:8090/control` | Every OpenAI-format request fails (`ok`, `503`, `429`, `500`, `slow`, `drop`); likewise `anthropic`, `gemini` |
