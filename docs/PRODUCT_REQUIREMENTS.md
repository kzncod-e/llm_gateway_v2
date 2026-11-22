# Product Requirements — LLM Gateway Platform

> One API. Multiple Providers. Controlled Cost. Reliable Routing.

## 1. Problem statement

Teams that build on large language models integrate OpenAI, Anthropic and Google Gemini directly. Each integration has its own request format, streaming protocol, error semantics and token accounting. The result:

- Provider API keys are copied into many applications and are hard to rotate or revoke.
- Nobody can answer "how much did team X spend on LLMs this month?" without reconciling provider invoices.
- A provider outage takes every dependent application down because there is no shared retry or fallback.
- Runaway jobs can exhaust a monthly budget in hours with no guardrail.

## 2. Product vision

A self-hosted gateway that sits between applications and LLM providers. Applications speak one OpenAI-compatible API with a gateway-issued key. The gateway owns provider credentials, routes each request to a provider, retries and falls back on failure, enforces per-team rate limits and budgets, and records every attempt with tokens and cost. A management console gives administrators and team owners control and visibility.

## 3. Target users

| Persona | Needs |
|---|---|
| **Platform administrator** | Configure providers, credentials, models, pricing and routing; create teams; see global spend and reliability. |
| **Team owner / developer** | Create and revoke API keys for their team, watch team usage and budget, test prompts in a playground, inspect request logs. |
| **Application (machine client)** | A stable, OpenAI-compatible HTTP API with streaming, predictable errors and low added latency. |

## 4. Functional requirements

### 4.1 Data plane (gateway)
- **FR-1** `POST /v1/chat/completions`, OpenAI-compatible request and response, both non-streaming JSON and SSE streaming.
- **FR-2** `GET /v1/models` lists models the caller may request.
- **FR-3** Authenticate requests with a gateway API key (`Authorization: Bearer gw-...`). Keys belong to exactly one team.
- **FR-4** Provider adapters for OpenAI, Anthropic and Gemini that translate requests, responses, streams, errors and usage.
- **FR-5** Routing strategies: explicit provider (`provider/model` or `X-Gateway-Provider` header), model-based (model name resolves to the provider(s) that serve it), and named routing policies with an ordered primary → fallback chain.
- **FR-6** Retries with exponential backoff and jitter, only for retryable failures (HTTP 408/429/500/502/503/504/529, timeouts, network errors). Configurable maximum attempts.
- **FR-7** Fallback to the next target in a route when a target is exhausted or its circuit is open.
- **FR-8** Per-provider circuit breaker with CLOSED, OPEN and HALF_OPEN states.
- **FR-9** Per-team requests-per-minute limit and concurrent-request limit, enforced in Redis.
- **FR-10** Per-team monthly budget (and optional daily budget) enforced with reservations so concurrent requests cannot overspend.
- **FR-11** Streaming failure policy: retry/fallback only before the first chunk reaches the client; afterwards send an SSE error event, close the stream and record the interrupted attempt.
- **FR-12** Every logical request gets a request ID returned in `X-Request-ID`.

### 4.2 Usage and cost
- **FR-13** Record every provider attempt: attempt number, provider, model, status, HTTP status, error, duration.
- **FR-14** Record usage per attempt: input, cached-input, cache-write, output and reasoning tokens, as reported by the provider. If the provider did not report usage, the usage status is `unknown`; it is never recorded as zero.
- **FR-15** Cost computed from a versioned pricing table (price per million tokens per category, effective-from date). The price version used is stored with the usage record.
- **FR-16** Interrupted requests with unknown usage are charged against the budget at their reserved (conservative) estimate and flagged as estimated.

### 4.3 Control plane (management API and console)
- **FR-17** Dashboard login (email + password), sessions in HttpOnly cookies, logout.
- **FR-18** Roles: platform `admin` and `member`; per-team roles `owner`, `developer`, `viewer`.
- **FR-19** Teams: create, edit, list, members, rate limits, budgets.
- **FR-20** API keys: create (raw key shown exactly once), list (prefix only), revoke.
- **FR-21** Providers: create/edit, set or rotate credentials (write-only), enable/disable, list models, health and circuit state.
- **FR-22** Models and pricing: CRUD for models; append-only pricing versions.
- **FR-23** Routing policies: alias → ordered targets, retry settings.
- **FR-24** Analytics: spend, requests, tokens and success rate over a date range; breakdowns by provider, model and team; time series.
- **FR-25** Request logs: searchable, filterable, paginated; detail view with all attempts, fallback history, usage, cost and errors.
- **FR-26** Playground: pick team/model, compose messages, set parameters, run streaming or non-streaming, see usage and cost. Runs through the real gateway engine.
- **FR-27** Reliability view: circuit states, error rates, fallback events, latency, failed requests.
- **FR-28** Audit log of administrative changes.

## 5. Non-functional requirements

| ID | Requirement |
|---|---|
| NFR-1 | Gateway overhead (excluding provider time) p95 < 15 ms on local hardware for non-streaming requests. |
| NFR-2 | Streaming chunks forwarded as they arrive; no full-response buffering. |
| NFR-3 | Raw gateway API keys are never stored or logged; only a keyed hash and display prefix are kept. |
| NFR-4 | Provider credentials encrypted at rest with AES-256-GCM; never returned by any API. |
| NFR-5 | Prompts and completions are not persisted by default. |
| NFR-6 | Budget enforcement remains correct under concurrent requests (verified by tests). |
| NFR-7 | Fail closed: if Postgres (budget) is unavailable the gateway rejects requests with 503; if Redis (rate limiting) is unavailable the behaviour is configurable, default fail closed. |
| NFR-8 | Prometheus metrics with bounded labels only; traces via OpenTelemetry. |
| NFR-9 | The whole stack starts with `docker compose up` and runs without real provider keys (bundled mock provider). |
| NFR-10 | Basic test suite runs without provider keys or network access to providers. |
| NFR-11 | Graceful shutdown drains in-flight requests and pending usage writes. |

## 6. MVP scope

Everything in sections 4 and 5, with these simplifications:
- Single gateway binary serving data plane and control plane (separate route groups and middleware).
- Text chat only (system/user/assistant messages, string or text-part content).
- Circuit-breaker state is per gateway instance (in memory).
- Single currency: USD.
- Routing policies are global (not per team).

## 7. Out of scope (MVP)

- Tool/function calling, vision/audio inputs, embeddings, image generation.
- Ollama and other providers (the adapter interface allows adding them).
- Distributed (Redis-backed) circuit-breaker state.
- Semantic caching of responses.
- SSO/OIDC, MFA, password reset by email.
- Multi-region deployment, Kubernetes manifests.
- Invoicing, multi-currency.
- Storing prompt/response bodies (a future opt-in per team).

## 8. Acceptance criteria

The MVP is complete when all of these are verified:

1. A user can sign in to the management dashboard.
2. An administrator can create teams.
3. An administrator or team owner can create gateway API keys; the raw key is shown once.
4. An administrator can configure multiple providers with encrypted credentials.
5. A client can call `POST /v1/chat/completions` with a gateway key.
6. Requests route to different providers based on model, explicit selection or routing policy.
7. A failing provider triggers fallback to the next target.
8. An open circuit breaker stops calls to an unhealthy provider until its cool-down passes.
9. Non-streaming responses work.
10. SSE streaming works, including usage at end of stream.
11. Token usage is recorded per attempt.
12. Estimated cost is calculated from versioned pricing.
13. Usage is attributed to the correct team and API key.
14. Concurrent requests cannot push a team past its budget (test with parallel requests).
15. Rate limiting returns 429 when the per-minute or concurrency limit is exceeded.
16. Request logs show every provider attempt.
17. Dashboard analytics are computed from real backend data.
18. Prometheus scrapes gateway metrics.
19. Grafana shows a provisioned gateway dashboard.
20. Automated backend and frontend tests pass.
21. `docker compose up` starts the full environment.
22. Documentation explains how to run, configure and test the system.
