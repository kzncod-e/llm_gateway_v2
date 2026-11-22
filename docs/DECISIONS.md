# Architecture Decision Records

Each record: context, decision, consequences. Status is *Accepted* unless noted.

## ADR-001 — Monorepo with one Go binary for both planes
**Context.** The spec separates data plane and control plane but warns against unnecessary microservices.
**Decision.** `service/` builds one binary with two route groups (`/v1` data plane, `/api/v1` control plane) sharing the DB pool and gateway engine. `web/` holds the Next.js console. Compose, docs and monitoring config live at the root.
**Consequences.** One deployable and one config surface. The planes are still separate packages, so splitting later is a new `main` package, not a rewrite.

## ADR-002 — Keep existing module name and layered folders
**Context.** The workspace already had module `service` with empty `internal/{handler,service,repository,model,router,database,dtp}`.
**Decision.** Keep the module name and the layered folders; add domain packages (`gateway`, `provider`, `breaker`, `ratelimit`, `budget`, `pricing`, `usage`, `security`, `observability`). `dtp` (empty, presumably a typo) is replaced by `dto`.
**Consequences.** Respects the existing convention; imports read `service/internal/...`.

## ADR-003 — pgx with hand-written SQL and an embedded migration runner
**Context.** Financial correctness needs explicit transactions and conditional updates; ORMs obscure those.
**Decision.** `pgx/v5` + `pgxpool`, SQL in repository files. Migrations are numbered `.sql` files embedded with `embed.FS`, applied under a Postgres advisory lock and tracked in `schema_migrations`.
**Consequences.** No migration tool dependency; migrations are forward-only (down migrations are not provided for the MVP).

## ADR-004 — Internal circuit breaker instead of a library
**Context.** Libraries such as `sony/gobreaker` exist. Our needs: per-provider breakers, our own failure classification, exact half-open probe accounting, state export to metrics and API.
**Decision.** ~150-line internal `breaker` package guarded by a mutex, with injectable clock for tests.
**Consequences.** Fully tested and tailored; per-instance state only. A Redis-backed shared state is a documented post-MVP extension.

## ADR-005 — Budget reservations in Postgres, not Redis
**Context.** The spec suggests Redis for temporary reservations. Reservations must be checked against *spent* money, which lives in Postgres. Keeping a spent counter in Redis means two sources of truth, and losing Redis data would silently allow overspend.
**Decision.** A `budget_periods` row per team and period holds `spent_nanos` and `reserved_nanos`. Reservation is a conditional `UPDATE ... WHERE spent + reserved + amount <= limit`. Settlement happens in the same transaction that writes the usage ledger. Reservations have an expiry and a sweeper.
**Consequences.** Exact, durable, concurrency-safe enforcement with one source of truth. Cost: two short row-locking writes per request per team. Requests from one team serialize briefly on that row; acceptable for the MVP (thousands of requests per second per team would need sharded counters). Redis is used for rate limiting and concurrency, where approximate durability is acceptable.

## ADR-006 — Money as integer nano-USD
**Context.** Per-token prices are tiny fractions of a cent; floats drift.
**Decision.** BIGINT nanos everywhere (DB, Go, API). Prices per million tokens in nanos. Cost uses a 128-bit intermediate and rounds half-up.
**Consequences.** Exact sums in SQL; the UI formats nanos to dollars.

## ADR-007 — Unknown usage is charged at the reservation estimate
**Context.** Interrupted streams and timeouts may have consumed tokens the provider never reported. The spec forbids recording them as zero.
**Decision.** `usage_status = unknown`, `cost_nanos = NULL`, and the budget is charged the reserved estimate (`budget_charge_nanos`), flagged `cost_status = estimated` on the request.
**Consequences.** Budgets err on the safe side; analytics show actual vs. estimated cost separately.

## ADR-008 — Teams with a budget cannot use unpriced models
**Context.** Without a price, cost cannot be computed and the budget could be bypassed.
**Decision.** Unpriced targets are skipped for teams with a budget (`pricing_not_configured` if none remain). Teams without a budget may use them; cost is recorded as unknown.
**Consequences.** Financial control fails closed; admins see a warning in the model list.

## ADR-009 — Stream commit point is the first content chunk
**Context.** Fallback mid-stream would splice two different generations together.
**Decision.** The gateway reads from the upstream until the first chunk is available, and only then writes SSE headers. Before that, failures are retried/fall back; after it, an SSE error event ends the stream.
**Consequences.** Time-to-first-byte to the client equals provider TTFB (no extra buffering), and retries are invisible to the client before commit.

## ADR-010 — Console talks to the control plane through a Next.js route-handler proxy
**Context.** Cookies must be same-origin; Next.js `rewrites` are fixed at build time, which breaks a single image across environments.
**Decision.** `app/api/[...path]/route.ts` forwards to `BACKEND_URL` at runtime, streaming request and response bodies.
**Consequences.** No CORS configuration; playground SSE passes through; backend URL is a runtime env var.

## ADR-011 — Playground uses a control-plane endpoint, not an API key
**Context.** Asking console users to paste a gateway key into the browser is poor UX and spreads keys.
**Decision.** `POST /api/v1/playground/chat` (session auth, team developer+) calls the same engine with `source = playground`, enforcing the team's rate limit and budget.
**Consequences.** Usage is attributed to the team with `api_key_id = NULL` and `user_id` set.

## ADR-012 — Bundled mock provider
**Context.** The stack must run and be testable without real provider keys.
**Decision.** `cmd/mockprovider` emulates the three wire formats with deterministic output and failure injection (by model name, e.g. `mock-fail-503`, `mock-slow`, `mock-drop-stream`). Compose seeds demo providers pointing at it when `SEED_DEMO=true`.
**Consequences.** Fallback, breaker and streaming behaviours are demonstrable end-to-end locally.

## ADR-013 — Rate-limited requests are not written to Postgres
**Context.** Logging every rejected request during abuse would amplify load on the database.
**Decision.** 429s from the gateway's own limiter are counted in Prometheus and access logs only. Requests that pass rate limiting (including budget rejections and upstream failures) are recorded in `gateway_requests`.
**Consequences.** Request logs show everything that consumed or could consume budget.

## ADR-014 — TanStack Table v8, not v9
**Context.** `@tanstack/react-table` 9.x became the `latest` tag with a reworked API (`createCoreRowModel`, table features).
**Decision.** Pin `^8`. Its API is stable and well documented; the console uses only core and sorted row models.
**Consequences.** Upgrading to v9 later is a contained change in `components/common/data-table.tsx`.

## ADR-015 — Dark-only console; standard class merging
**Context.** The "Charcoal Sand" identity is a dark palette. The shadcn CLI also installed `next-themes` and a young `cn` package (0.5.x) duplicating `clsx` + `tailwind-merge`.
**Decision.** Tokens live on `:root` with `color-scheme: dark`; no theme switcher. `cn()` uses `clsx` + `tailwind-merge`; `cn` and `next-themes` were removed.
**Consequences.** Fewer dependencies; a light theme would need a second token set.

## ADR-016 — Fallback on provider misconfiguration; per-account login throttle
**Context.** A 401/403 (revoked key) or 404 (model not offered) from one provider says nothing about the next provider. Separately, behind the console's same-origin proxy every user shares one client IP.
**Decision.** 401/403/404 are not retried on the same target but move to the next target; 400/413/422 stop the request. Login attempts are limited to 10 per minute per (IP, email).
**Consequences.** A broken credential on the primary does not cause an outage when a fallback exists. One user's typos cannot lock out others.

## ADR-017 — Default output cap is sent upstream
**Context.** A reservation must bound the request's worst-case cost, but clients often omit `max_tokens`, leaving provider defaults that can be very large.
**Decision.** When omitted, the engine sends `DEFAULT_MAX_OUTPUT_TOKENS` (4096) to the provider and reserves for it.
**Consequences.** Long generations need an explicit `max_tokens`. Documented in the API reference and `.env.example`.

## ADR-018 — Each attempt with unknown usage is charged its own estimate
**Context.** A request can time out on several attempts, each of which may have consumed tokens.
**Decision.** Every attempt with unknown usage adds its target's worst-case estimate to the budget charge. The reservation covers a single attempt, so settlement can push spend past the limit by the additional estimates.
**Consequences.** Budgets err toward over-counting. Spend can exceed a limit by at most (attempts − 1) × estimate in this rare case; reserving for every possible attempt would reject most legitimate requests near the limit.
