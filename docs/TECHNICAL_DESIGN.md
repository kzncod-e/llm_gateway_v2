# Technical Design

## 1. Technology choices

| Concern | Choice | Why |
|---|---|---|
| HTTP framework | Gin (already in the workspace) | Mature, fast routing, middleware model; streaming via `http.Flusher` underneath. |
| Postgres driver | `jackc/pgx/v5` + `pgxpool` | Fastest maintained driver, native types, tracer hook for OpenTelemetry. No ORM: queries are explicit SQL. |
| Migrations | Small embedded runner (`internal/database/migrate.go`) | `embed.FS` + `schema_migrations` table + advisory lock. Avoids a dependency for ~60 lines of logic; migrations run on startup or via `api migrate`. |
| Redis client | `redis/go-redis/v9` | Official client, Lua scripting, OTel hook (`redisotel`). |
| Logging | `log/slog` (JSON) | Standard library, structured. |
| Metrics | `prometheus/client_golang` | De-facto standard. |
| Tracing | OpenTelemetry SDK + OTLP/HTTP exporter, `otelhttp` for outbound provider calls | Vendor-neutral; Jaeger in Compose to view traces. |
| Password hashing | bcrypt (`golang.org/x/crypto`) | Standard for low-volume interactive logins. |
| API key hashing | HMAC-SHA256 with server pepper | Keys are 256-bit random, so a fast keyed hash is safe and allows O(1) lookup. |
| Credential encryption | AES-256-GCM (stdlib) | Authenticated encryption; key from `ENCRYPTION_KEY`. |
| IDs | UUID v7 (`google/uuid`) | Time-ordered, index-friendly, safe to expose. |
| Tests | `testing`, `httptest`, `alicebob/miniredis` (Redis Lua in-process), real Postgres for integration tests (skipped when `TEST_DATABASE_URL` is unset) | Deterministic, no provider keys. |
| Frontend | Next.js 16 App Router (Cache Components), TypeScript, Tailwind v4, shadcn/ui (Radix), TanStack Query v5, TanStack Table v8, RHF + Zod 4, Recharts 3, Lucide | As specified. Zustand is not needed: server state lives in TanStack Query, UI state in components. Table v8 rather than v9: ADR-014. |
| Traces backend | Jaeger all-in-one | Accepts OTLP natively, single container. |

## 2. Backend architecture

```
service/
  cmd/api/              main: serve | migrate | seed
  cmd/mockprovider/     serves internal/mockllm
  internal/
    app/                dependency wiring shared by main and end-to-end tests
    mockllm/            mock OpenAI/Anthropic/Gemini wire formats with failure injection
    seed/               demo data
    testutil/           per-test Postgres schemas for integration tests
    config/             env parsing + validation
    database/           pgx pool, tracer, migration runner, migrations/*.sql
    model/              domain types (Team, APIKey, Provider, Model, Pricing, ...)
    repository/         SQL per aggregate (teams.go, apikeys.go, providers.go, requests.go, ...)
    security/           AES-GCM secret box, API key generation/hashing, session tokens, passwords
    provider/           Adapter interface, unified types, error classification
      openai/ anthropic/ gemini/
    gateway/            engine (orchestration), routing snapshot, retry/backoff, streaming writer
    breaker/            circuit breaker registry
    ratelimit/          Redis sliding-window + concurrency limiter
    budget/             reservation ledger
    pricing/            cost calculation
    usage/              recorder (settlement transaction)
    service/            control-plane use cases (auth, teams, keys, providers, analytics, ...)
    handler/            Gin handlers: dataplane/*.go, controlplane/*.go
    dto/                request/response bodies for the control plane
    router/             route registration + middleware chains
    observability/      metrics, tracing setup, logging, middleware
```

Dependencies point inwards: `handler → service/gateway → repository/provider/...→ model`. Handlers never contain business rules; adapters never know about teams or budgets.

### 2.1 Data-plane middleware chain
`recover → requestID → securityHeaders → otel span → metrics → bodyLimit(1 MiB) → apiKeyAuth → handler`. The rate limiter and budget run inside the engine so the playground gets the same enforcement.

### 2.2 Gateway engine (per request)
1. Validate the unified request (`messages` non-empty, roles valid, numeric ranges).
2. Rate limit: RPM check and concurrency lease (released by `defer`).
3. Resolve route → `[]Target{Provider, Model, Pricing, Adapter}`; 404 if empty.
4. Estimate worst-case cost and reserve budget (if the team has a budget).
5. Attempt loop (section 5).
6. Write response (JSON) or stream.
7. Record: one transaction writes `gateway_requests`, `provider_attempts`, `usage_records` and settles the reservation. Runs with `context.WithoutCancel` and a 5 s timeout.

### 2.3 Configuration snapshot
`gateway.Catalog` holds providers (with decrypted keys, in memory only), models, current prices and routing policies. It is rebuilt from Postgres at startup, after every control-plane mutation, and every 30 s. Readers take an atomic pointer to an immutable snapshot, so the hot path performs no locking or queries for configuration.

## 3. Database design

Money is stored as **BIGINT nano-USD** (1 USD = 10⁹). Prices are stored per million tokens in nanos, e.g. $2.50 / 1M tokens = `2500000000`. Integer arithmetic avoids float drift; the 128-bit intermediate in `pricing.Cost` prevents overflow.

### 3.1 Tables

| Table | Purpose | Key columns |
|---|---|---|
| `users` | Console users | `email` (unique, lower-cased), `password_hash`, `role` (`admin`/`member`), `disabled_at` |
| `user_sessions` | Console sessions | `token_hash` (unique), `expires_at` |
| `teams` | Tenants | `slug` (unique), `rpm_limit`, `concurrency_limit` (NULL = default), `archived_at` |
| `team_members` | Membership | PK (`team_id`, `user_id`), `role` (`owner`/`developer`/`viewer`) |
| `api_keys` | Gateway keys | `key_hash` (unique), `prefix` (display), `revoked_at`, `expires_at`, `last_used_at` |
| `providers` | Provider instances | `slug` (unique), `type`, `base_url`, `api_key_ciphertext`, `api_key_last4`, `enabled`, `priority`, `timeout_ms` |
| `models` | Models offered by a provider | unique (`provider_id`, `name`), `enabled` |
| `model_pricing` | Append-only price versions | unique (`model_id`, `effective_from`), per-category prices |
| `routing_policies` | Alias → ordered targets | `alias` (unique), retry settings |
| `routing_policy_targets` | Ordered targets | PK (`policy_id`, `position`), `model_id` |
| `team_budgets` | Budget configuration | PK `team_id`, `monthly_limit_nanos`, `daily_limit_nanos`, `warn_threshold_pct` |
| `budget_periods` | Running counters | PK (`team_id`, `period_type`, `period_start`), `spent_nanos`, `reserved_nanos` |
| `budget_reservations` | Reservation ledger | `status` (`active`/`settled`/`released`), `expires_at` |
| `gateway_requests` | One row per logical request | status, totals, final provider/model, latency, cost |
| `provider_attempts` | One row per provider call | attempt number, status, http status, error, duration |
| `usage_records` | Financial ledger, 1:1 with attempts that reached a provider | token categories, `usage_status`, `pricing_id`, `cost_nanos` |
| `audit_logs` | Admin actions | actor, action, target, JSON metadata |

`usage_records` is separate from `provider_attempts` on purpose: attempts are an operational log; usage is the financial ledger with its own retention and index needs. `gateway_requests` carries denormalized totals so the request-log list needs no joins.

### 3.2 Indexes (driven by queries)
- Request log list: `gateway_requests (team_id, created_at DESC, id DESC)` and `(created_at DESC, id DESC)` for keyset pagination; `(status, created_at)` for failure filters.
- Analytics: `usage_records (created_at)`, `(team_id, created_at)`; group-bys on provider/model scan the time-range index.
- Attempts: `provider_attempts (request_id)`; `(provider_id, started_at)` for reliability.
- Budget: PK lookups; `budget_reservations (status, expires_at) WHERE status = 'active'` for the sweeper.
- Pricing: `model_pricing (model_id, effective_from DESC)`.

Partitioning of `gateway_requests` / `usage_records` by month is the next step at scale; not needed for the MVP.

## 4. Redis responsibilities

| Key | Type | Purpose | TTL |
|---|---|---|---|
| `rl:rpm:{team}:{minute}` | string counter | Sliding-window RPM (current + weighted previous minute) | 120 s |
| `rl:conc:{team}` | sorted set (lease id → expiry) | Concurrency leases; expired leases pruned on each acquire | refreshed |
| `rl:login:{ip}:{minute}` | counter | Login brute-force protection | 120 s |

Both rate-limit operations are single Lua scripts, so they are atomic under concurrency. Redis is **not** used for money (see ADR-005).

## 5. Provider adapter design

```go
type Adapter interface {
    Type() string
    ChatCompletion(ctx context.Context, cfg Config, req *ChatRequest) (*ChatResponse, error)
    StreamChatCompletion(ctx context.Context, cfg Config, req *ChatRequest) (Stream, error)
}

type Stream interface {
    Recv() (*StreamChunk, error) // io.EOF at clean end
    Usage() Usage                // final usage (Status=unknown if never reported)
    Close() error
}

type Usage struct {
    Status            UsageStatus // reported | unknown | none
    InputTokens       int64       // uncached input
    CachedInputTokens int64       // cache reads
    CacheWriteTokens  int64       // Anthropic cache creation
    OutputTokens      int64       // includes reasoning/thinking
    ReasoningTokens   int64       // informational subset of output
}

type Error struct {  // every adapter error is normalized to this
    Kind       ErrorKind // rate_limited | server | timeout | network | auth | bad_request | not_found | canceled ...
    StatusCode int
    Message    string
    RetryAfter time.Duration
    ProviderCode string
}
```

`provider.IsRetryable(err)` decides retry eligibility from `Kind`, never from string matching.

Normalization per provider (from the official API references):

| | OpenAI Chat Completions | Anthropic Messages | Gemini `generateContent` |
|---|---|---|---|
| Endpoint | `POST /v1/chat/completions` | `POST /v1/messages` | `POST /v1beta/models/{m}:generateContent`, `:streamGenerateContent?alt=sse` |
| Auth | `Authorization: Bearer` | `x-api-key`, `anthropic-version: 2023-06-01` | `x-goog-api-key` |
| System prompt | message with role `system` | top-level `system` | `systemInstruction.parts[].text` |
| Roles | system/user/assistant | user/assistant (consecutive same-role merged) | user/model |
| Max tokens | `max_completion_tokens` | `max_tokens` (required; default applied) | `generationConfig.maxOutputTokens` |
| Stop | `stop` | `stop_sequences` | `generationConfig.stopSequences` |
| Usage | `usage.prompt_tokens` (includes `prompt_tokens_details.cached_tokens`), `completion_tokens` (includes `completion_tokens_details.reasoning_tokens`) | `input_tokens` (excludes cache), `cache_read_input_tokens`, `cache_creation_input_tokens`, `output_tokens` | `promptTokenCount` (includes `cachedContentTokenCount`), `candidatesTokenCount`, `thoughtsTokenCount` (billed as output) |
| Stream usage | final chunk when `stream_options.include_usage=true` | `message_start.message.usage` + `message_delta.usage` | `usageMetadata` on chunks (last is final) |
| Finish reason mapping | as-is | `end_turn`/`stop_sequence`→`stop`, `max_tokens`→`length` | `STOP`→`stop`, `MAX_TOKENS`→`length`, `SAFETY`/`RECITATION`/…→`content_filter` |
| Retryable statuses | 408, 429, 500, 502, 503, 504 | same + 529 (overloaded) | same |

Adding a provider = new package implementing `Adapter` + registering its type string. Nothing in the engine changes.

## 6. Routing

Resolution order for a requested `model` string:
1. **Routing policy alias** — `routing_policies.alias = model` → its ordered targets (fallback chain).
2. **Explicit provider** — `provider-slug/model-name`, or header `X-Gateway-Provider: slug` → exactly that target.
3. **Model-based** — every enabled model with `name = model` on an enabled provider, ordered by `providers.priority` (lower first). More than one match gives automatic fallback across providers.

When a client omits `max_tokens`, the engine sends `DEFAULT_MAX_OUTPUT_TOKENS` upstream so the budget reservation is a true upper bound (ADR-017).

Retry defaults (overridable per policy): 2 attempts per target, backoff `min(max, base·2^(n-1))` with full jitter (base 250 ms, max 2 s), `Retry-After` honoured up to 5 s, at most 6 attempts in total, per-attempt timeout = provider `timeout_ms` (default 60 s), overall request deadline 120 s (streams: 10 min).

## 7. Circuit breaker

Small internal implementation (ADR-004). Per provider:
- **CLOSED**: count consecutive failures; at `failure_threshold` (default 5) → OPEN.
- **OPEN**: reject immediately for `open_duration` (default 30 s), then the next `Allow()` moves to HALF_OPEN.
- **HALF_OPEN**: allow up to `half_open_max` (default 1) concurrent probes; one success → CLOSED, one failure → OPEN.

Failures counted: timeouts, network errors, 5xx, 529, 429, 401/403. Client errors (400/404/422) and client cancellations are neutral. All transitions happen under a mutex; `Allow()` returns a `done(success bool)` callback so that probe accounting is exact. State is exported as a gauge (0=closed, 1=half-open, 2=open) and in the reliability API.

## 8. Budget reservation

```sql
-- reserve (one statement per period type, inside a transaction)
INSERT INTO budget_periods (team_id, period_type, period_start) VALUES (...) ON CONFLICT DO NOTHING;
UPDATE budget_periods
   SET reserved_nanos = reserved_nanos + $amount
 WHERE team_id = $team AND period_type = $type AND period_start = $start
   AND spent_nanos + reserved_nanos + $amount <= $limit
RETURNING spent_nanos, reserved_nanos;
-- zero rows → budget exceeded → rollback
INSERT INTO budget_reservations (...status 'active', expires_at = now() + 15 min);
```

The conditional `UPDATE` takes a row lock, so concurrent reservations for the same team serialize on that row and can never jointly exceed the limit. Settlement in the recorder transaction: `reserved -= reserved_amount, spent += charge` and the reservation becomes `settled`. A sweeper releases `active` reservations past `expires_at` (crash recovery). The estimate is deliberately pessimistic: `ceil(chars/3) + 8 per message` input tokens and `max_tokens` (default 4096) output tokens, priced at the most expensive target in the route. Teams with a budget cannot route to a model that has no price (fail closed, `pricing_not_configured`).

## 9. API contracts

Full examples in `API_REFERENCE.md`. Summary:

**Data plane** (`Authorization: Bearer gwk_...`)
- `POST /v1/chat/completions` — OpenAI request: `model`, `messages[{role, content}]`, `stream`, `temperature`, `top_p`, `max_tokens`/`max_completion_tokens`, `stop`, `user`. Response: OpenAI `chat.completion` / `chat.completion.chunk`. Extra response headers: `X-Request-ID`, `X-Gateway-Provider`, `X-Gateway-Model`, `X-Gateway-Attempts`.
- `GET /v1/models` — OpenAI `list` of model ids (aliases, `name`, `slug/name`).

**Errors** (both planes): `{"error": {"type": "...", "code": "...", "message": "..."}}` with codes: `invalid_request` 400, `invalid_api_key` 401, `forbidden` 403, `model_not_found` 404, `budget_exceeded` 402, `rate_limit_exceeded` 429, `concurrency_limit_exceeded` 429, `upstream_error` 502, `no_healthy_provider` 503, `service_unavailable` 503.

**Control plane** (`/api/v1`, session cookie): auth, users, teams, members, budgets, API keys, providers (+ credentials, test), models, pricing, routing policies, analytics (summary, timeseries, breakdown), requests (list, detail), reliability, playground, audit logs, settings. JSON only; list endpoints return `{ "data": [...], "next_cursor": "..." }`.

## 10. Authentication and authorization

- **Console login**: bcrypt (cost 12) password check, throttled to 10 attempts per minute per IP and email (ADR-016). Unknown emails take the same time as wrong passwords. Session token = 32 random bytes, stored as SHA-256, cookie `gw_session` (`HttpOnly`, `SameSite=Lax`, `Secure` when `COOKIE_SECURE=true`), 7-day lifetime.
- **CSRF**: mutating control-plane requests must have `Content-Type: application/json`, which a cross-site form cannot send without a CORS preflight (and no CORS is enabled). Combined with `SameSite=Lax`.
- **Roles**: `admin` can do everything. `member` sees only teams they belong to. Team `owner` manages keys and members and edits the team's name and description (rate limits, budgets and archival are admin-only); `developer` can create keys and use the playground; `viewer` is read-only. Providers, models, pricing, routing and users are admin-only.
- **Team isolation**: every team-scoped repository query takes the team ID; services resolve the allowed team set from the session before querying analytics or logs.
- **Gateway keys**: `gwk_` + 43-char base64url (256 bits). Stored: HMAC-SHA256(pepper, key) and a 12-char display prefix. Raw key returned once. Lookups cached in-process for 30 s (revocation is immediate on the instance that revoked; ≤30 s elsewhere).
- **Bootstrap**: if no users exist, `ADMIN_EMAIL`/`ADMIN_PASSWORD` creates the first admin at startup.

## 11. Security model

- Provider keys: AES-256-GCM, random 96-bit nonce, `key_version` column for rotation. Decrypted only into the in-memory catalog. API responses show `api_key_last4` only.
- Secrets only from environment variables; the service refuses to start with missing/short `ENCRYPTION_KEY`, `API_KEY_PEPPER`.
- Request body limit 1 MiB (data plane), 256 KiB (control plane). Server read-header timeout 10 s, idle 120 s; per-request deadlines in the engine.
- Security headers: `X-Content-Type-Options`, `X-Frame-Options: DENY`, `Referrer-Policy`, `Content-Security-Policy` (console), `Cache-Control: no-store` on API responses.
- Logs never contain API keys, credentials, session tokens or prompt/response bodies. A log-scrubbing test asserts this for the request path.
- Validation: strict JSON decoding of control-plane bodies (`DisallowUnknownFields`), length limits on names, URL validation of provider base URLs (http/https only).

## 12. Frontend architecture

```
../llm_gateway_dashboard/src/
  app/
    (auth)/login/page.tsx
    (console)/layout.tsx           sidebar + header shell
    (console)/overview|playground|usage|providers|teams|requests|reliability|settings/...
    api/[...path]/route.ts         streaming proxy to the Go control plane
  proxy.ts                         redirects to /login without a session cookie (Next 16 "proxy" = middleware)
  components/ui/                   shadcn primitives (themed)
  components/layout/               sidebar, header, page-header
  components/common/               stat-card, data-table, states (loading/empty/error), status badges
  features/<feature>/              api.ts (fetchers), hooks.ts (TanStack Query), components/*
  lib/                             api client, formatters (money/tokens/dates), query client
```

Server Components render page shells; interactive tables, charts and forms are Client Components that use TanStack Query against `/api/v1/*` through the proxy. Design tokens (Charcoal Sand palette) are CSS variables in `globals.css` consumed by Tailwind v4 `@theme`.

## 13. Observability

**Metrics** (labels are bounded: provider slug, model name from catalog, status class, error kind, route template):
- `llm_gateway_requests_total{status, stream}`
- `llm_gateway_request_duration_seconds{stream}` (histogram)
- `llm_gateway_request_errors_total{code}`
- `llm_gateway_provider_requests_total{provider, model, outcome}`
- `llm_gateway_provider_request_duration_seconds{provider}`
- `llm_gateway_fallback_total{from_provider, to_provider}`
- `llm_gateway_circuit_breaker_state{provider}`
- `llm_gateway_rate_limited_total{reason}`
- `llm_gateway_budget_rejections_total`
- `llm_gateway_tokens_total{provider, type}`
- `llm_gateway_stream_time_to_first_byte_seconds{provider}`
- `llm_gateway_http_requests_total{method, route, code}` (control plane + data plane, route template only)
- Go runtime/process collectors.

Team IDs are deliberately **not** metric labels (unbounded over time); team analytics come from Postgres.

**Traces**: server span per HTTP request; child spans `gateway.ratelimit`, `gateway.budget.reserve`, `gateway.attempt` (attributes: provider, model, attempt number, outcome), outbound HTTP via `otelhttp`, `db.*` spans via a pgx tracer, Redis via `redisotel`, `gateway.record`.

**Logs**: JSON `slog`, one access line per request with request_id, team_id, status, latency; attempt-level warnings on failure.
