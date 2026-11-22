# LLM Gateway Platform

**One API. Multiple Providers. Controlled Cost. Reliable Routing.**

A self-hosted gateway between your applications and LLM providers (OpenAI, Anthropic, Google Gemini). Applications call one OpenAI-compatible API with a gateway key. The gateway owns the provider credentials, routes each request, retries and falls back on failure, enforces per-team rate limits and budgets, and records every provider attempt with tokens and cost. A management console provides control and visibility.

```
client ──► /v1/chat/completions ──► auth ─► rate limit ─► budget reservation ─► route ─► provider attempts ─► response / SSE
                                                                    (retry · fallback · circuit breaker)        │
                                                                                                    usage + cost ledger (Postgres)
```

## Features

- OpenAI-compatible `POST /v1/chat/completions` (JSON and SSE streaming) and `GET /v1/models`
- Adapters for OpenAI Chat Completions, Anthropic Messages and Gemini `generateContent`, with normalized responses, errors and token usage (including cached-input and cache-write tokens)
- Routing by policy alias (ordered fallback chain), by explicit provider (`provider/model` or `X-Gateway-Provider`) or by model name with provider priority
- Retries with exponential backoff, full jitter and `Retry-After`; per-provider circuit breaker (closed / open / half-open)
- Streaming that fails over before the first chunk and reports an SSE error event after it
- Per-team requests-per-minute and concurrency limits (Redis), monthly and daily budgets with reservation-based enforcement (Postgres)
- Versioned model pricing; costs in integer nano-USD; unknown usage is never treated as free
- Request logs with every provider attempt; analytics by provider, model and team
- Console: overview, playground, usage analytics, providers and routing, teams, keys and budgets, request logs, reliability, settings
- Prometheus metrics, a provisioned Grafana dashboard and OpenTelemetry traces (Jaeger)
- A bundled mock provider, so everything runs without real provider keys

## Quick start

Requirements: Docker with Compose v2, `openssl`.

The management dashboard is in the sibling repository `../llm_gateway_dashboard`; clone both repositories side by side before starting the full Compose stack.

```sh
./scripts/gen-env.sh          # creates .env with random secrets and prints the console login
docker compose up --build -d
```

| Service | URL |
|---|---|
| Console | http://localhost:3000 (login printed by `gen-env.sh`; email `admin@example.com`) |
| Gateway API | http://localhost:8080 |
| Grafana | http://localhost:3001 (user `admin`, password `GRAFANA_ADMIN_PASSWORD` in `.env`) |
| Prometheus | http://localhost:9090 |
| Jaeger | http://localhost:16686 |
| Mock provider | http://localhost:8090 |

With `SEED_DEMO=true` (the default in `.env.example`) the gateway seeds three mock providers, models with demo prices, three routing policies (`smart`, `fast`, `fallback-demo`) and a "Demo Team" with a $25 monthly budget and the API key from `DEMO_API_KEY`.

```sh
KEY=$(grep '^DEMO_API_KEY=' .env | cut -d= -f2-)

# Non-streaming; the primary target always fails, so this shows fallback
curl -s localhost:8080/v1/chat/completions -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"fallback-demo","messages":[{"role":"user","content":"Explain microservices architecture"}]}' -i

# Streaming with final usage
curl -N localhost:8080/v1/chat/completions -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"smart","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}'
```

Any OpenAI SDK works by pointing its base URL at the gateway:

```python
from openai import OpenAI
client = OpenAI(base_url="http://localhost:8080/v1", api_key=KEY)
client.chat.completions.create(model="smart", messages=[{"role": "user", "content": "hi"}])
```

To use real providers, add them under **Providers** in the console (credentials are encrypted at rest), add models and pricing, and create a routing policy or call `provider-slug/model` directly.

## Repository layout

| Path | Contents |
|---|---|
| `service/` | Go gateway (Gin, pgx, go-redis). Entry points in `cmd/api` and `cmd/mockprovider` |
| `../llm_gateway_dashboard/` | Separate Next.js 16 management dashboard; Compose builds it from the sibling checkout |
| `deploy/` | Prometheus config, Grafana provisioning and dashboard, example Nginx config |
| `docs/` | Requirements, architecture, design, decisions, API, schema, development and deployment guides |
| `scripts/` | `gen-env.sh` |
| `PROJECT_STATUS.md` | Milestone tracking and known limitations |

## Testing

```sh
# Backend: unit tests always run; integration and end-to-end tests run when TEST_DATABASE_URL is set
cd service && go test -race ./...
TEST_DATABASE_URL="postgres://user@localhost:5432/llm_gateway_test?sslmode=disable" go test -race ./...

# Frontend
cd ../llm_gateway_dashboard && pnpm lint && pnpm typecheck && pnpm test

# Browser end-to-end (needs the stack running)
cd ../llm_gateway_dashboard && pnpm build && E2E_PASSWORD=<admin password> pnpm test:e2e
```

None of the tests need real provider keys. See [docs/DEVELOPMENT_GUIDE.md](docs/DEVELOPMENT_GUIDE.md) for details.

## Documentation

- [Product requirements](docs/PRODUCT_REQUIREMENTS.md)
- [System architecture](docs/SYSTEM_ARCHITECTURE.md)
- [Technical design](docs/TECHNICAL_DESIGN.md)
- [Architecture decisions](docs/DECISIONS.md)
- [API reference](docs/API_REFERENCE.md)
- [Database schema](docs/DATABASE_SCHEMA.md)
- [Development guide](docs/DEVELOPMENT_GUIDE.md)
- [Deployment guide](docs/DEPLOYMENT_GUIDE.md)
- [Implementation plan](docs/IMPLEMENTATION_PLAN.md)
