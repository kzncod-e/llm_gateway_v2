# Project Status

Last updated: 2026-11-23. **MVP complete**: all milestones done and all 22 acceptance criteria verified.

| Milestone | Status | Notes |
|---|---|---|
| M0 Workspace discovery | Done | Existing Go/Gin skeleton in `service/` kept (module name, layered folders). |
| M1 Product requirements | Done | `docs/PRODUCT_REQUIREMENTS.md` |
| M2 System architecture | Done | `docs/SYSTEM_ARCHITECTURE.md` |
| M3 Technical design | Done | `docs/TECHNICAL_DESIGN.md` |
| M4 Implementation plan | Done | `docs/IMPLEMENTATION_PLAN.md`, `docs/DECISIONS.md` (ADR-001…018) |
| M5 Infrastructure | Done | Compose: postgres, redis, mock-llm, api, web, prometheus, grafana, jaeger. Cold start from empty volumes verified. |
| M6 Backend core | Done | Config, migrations, logging, health, auth/sessions, RBAC, teams, API keys, provider registry. |
| M7 Gateway engine | Done | OpenAI, Anthropic, Gemini adapters; routing; retry/backoff; fallback; circuit breaker; JSON + SSE. |
| M8 Usage & cost | Done | Versioned pricing, nano-USD costs, usage ledger, Postgres budget reservations, Redis rate limits. |
| M9 Frontend | Done | All eight sections against real APIs; Charcoal Sand design system. |
| M10 Observability | Done | Prometheus metrics, provisioned Grafana dashboard, OTel traces (HTTP, attempts, DB, Redis) verified with live traffic. |
| M11 Testing & hardening | Done | Unit, integration, API end-to-end, browser end-to-end, and live failure injection. |
| M12 Documentation | Done | README, API reference, schema, development and deployment guides. |

## Acceptance criteria

| # | Criterion | Evidence |
|---|---|---|
| 1 | Dashboard access | Login → overview (Playwright) |
| 2 | Admin creates teams | Playwright + `TestEndToEnd` |
| 3 | Admin creates API keys | Playwright: key shown once, only prefix listed afterwards |
| 4 | Multiple providers configured | Seeded OpenAI/Anthropic/Gemini (mock); provider CRUD with encrypted credentials |
| 5 | Unified chat completion API | All three providers via `/v1/chat/completions` |
| 6 | Routing to different providers | Policy, explicit and model-based routing tests |
| 7 | Fallback on failure | `fallback-demo`: 3 attempts → anthropic-mock (API, browser, live) |
| 8 | Circuit breaker | Engine + e2e tests; live outage: gauge = 2, policy skips the provider, reset restores |
| 9 | Non-streaming responses | Tests + live curl |
| 10 | SSE streaming | Tests; live: first event after 45 ms through the console proxy |
| 11 | Token usage recorded | Per-attempt `usage_records` incl. cached/cache-write/reasoning |
| 12 | Cost calculated | Versioned pricing, `cost_nanos` in responses and ledger |
| 13 | Usage attributed to team | Team-filtered logs and analytics; isolation tests |
| 14 | Budgets safe under concurrency | 100 concurrent reservations → exactly 10 succeed (real Postgres) |
| 15 | Rate limiting | RPM and concurrency tests; live 429 |
| 16 | Logs show attempts | Request detail with fallback history (browser test) |
| 17 | Analytics from real data | Summary, timeseries and breakdown endpoints; screenshots reviewed |
| 18 | Prometheus collects metrics | Target up; queries return live counts |
| 19 | Grafana shows health | "LLM Gateway" dashboard provisioned |
| 20 | Automated tests pass | `go test -race ./...`, `pnpm lint/typecheck/test`, `pnpm test:e2e` |
| 21 | Compose starts everything | Cold start from empty volumes verified |
| 22 | Docs explain how to run | `README.md`, `docs/DEVELOPMENT_GUIDE.md`, `docs/DEPLOYMENT_GUIDE.md` |

## Failure scenarios exercised

Provider timeout · provider 429 with Retry-After · provider 503 · Anthropic 529 · Redis down (503, recovers) · Postgres down (503, recovers) · client disconnect mid-stream (recorded `client_cancelled`, charged at the estimate) · provider disconnect mid-stream (SSE error event, no `[DONE]`) · unknown usage · budget exhausted (402) · concurrent requests · login brute force.

## Known limitations

- Circuit-breaker state is per gateway instance (in memory).
- An attempt with unknown usage is charged its full estimate; several such attempts in one request can push spend past a limit by those extra estimates (ADR-018).
- Clients that omit `max_tokens` are capped at `DEFAULT_MAX_OUTPUT_TOKENS` (ADR-017).
- Text chat only: no tools/function calling, images, audio or embeddings. No Ollama adapter yet.
- Gemini long-context (>200k tokens) price tiers are not modelled.
- Demo prices in the seed are illustrative; check provider pricing pages before using real providers.
- No Kubernetes manifests; Nginx config is an example and not part of the default Compose stack.

## Suggested next steps

Redis-backed shared breaker state · Ollama adapter · tool calling passthrough · monthly partitioning of request/usage tables · budget alert notifications · SSO.
