# Deployment Guide

The MVP ships as two container images and standard dependencies:

| Component | Image | Port | State |
|---|---|---|---|
| Gateway (`service/Dockerfile`) | distroless, non-root, contains `/api` and `/mockprovider` | 8080 | Stateless (circuit-breaker state is in memory per instance) |
| Dashboard (`../llm_gateway_dashboard/Dockerfile`) | Node 24 alpine, Next.js standalone, non-root | 3000 | Stateless |
| PostgreSQL | 16+ | 5432 | Configuration, usage ledger, budgets |
| Redis | 7+ | 6379 | Rate-limit counters (ephemeral; persistence not required) |
| Prometheus, Grafana, OTLP collector or Jaeger | optional | | Observability |

## Single host with Docker Compose

The repository's `docker-compose.yml` is a working single-host deployment when the sibling `../llm_gateway_dashboard` repository is checked out.

```sh
./scripts/gen-env.sh
# edit .env: APP_ENV=production, SEED_DEMO=false, COOKIE_SECURE=true, TRUSTED_PROXIES=<proxy CIDR>
docker compose up -d --build
```

For production on one host:

1. **Put TLS in front.** Use the example [`deploy/nginx/nginx.conf`](../deploy/nginx/nginx.conf): it terminates TLS, disables proxy buffering for SSE, extends read timeouts for long streams and blocks `/metrics` from the internet. Set `COOKIE_SECURE=true` and set `TRUSTED_PROXIES` to the proxy's network so client IPs (used for login throttling and audit logs) are correct.
2. **Do not publish internal ports.** The Compose file binds every port to `127.0.0.1` only. Keep it that way, or remove the `ports:` entries for Postgres, Redis, Prometheus and Jaeger entirely.
3. **Turn off demo data.** `SEED_DEMO=false` and remove `DEMO_API_KEY`. The demo seeds only when no providers exist, but a production database should start clean.
4. **Back up Postgres.** It holds the financial ledger. Redis needs no backup.

## Secrets

| Secret | Notes |
|---|---|
| `ENCRYPTION_KEY` | 32 random bytes, base64. Losing it makes stored provider credentials unreadable (re-enter them in the console). Rotating it requires re-entering credentials; `key_version` and the ciphertext version byte are in place for a future re-encryption tool |
| `API_KEY_PEPPER` | Changing it invalidates **every** gateway API key |
| `POSTGRES_PASSWORD`, `GRAFANA_ADMIN_PASSWORD`, `ADMIN_PASSWORD` | Standard credentials. `ADMIN_PASSWORD` is used only to create the first admin; change it in the console afterwards |

Provide secrets through your platform's secret store (Docker secrets, Kubernetes Secrets, a vault) as environment variables. Never bake them into images. `.env` is git-ignored and created with mode 600.

## Multiple gateway instances

The gateway can run behind a load balancer as N replicas:

- Rate limits and concurrency leases are shared through Redis (atomic Lua scripts).
- Budget reservations are row-locked in Postgres, so they are correct across instances.
- Migrations take a Postgres advisory lock; any instance can run them on start.
- The routing catalog refreshes every `CATALOG_REFRESH` (30 s) and immediately on the instance that made a change.
- API-key revocations take effect immediately on the instance that revoked and within 30 s elsewhere (auth cache TTL).
- **Circuit breakers are per instance.** Each replica learns provider health independently, which is acceptable for the MVP (each opens after 5 consecutive failures).

Size the Postgres pool (`pool_max_conns` in `DATABASE_URL`, default 20 per instance) to `replicas × pool ≤ max_connections` minus headroom.

## Graceful shutdown

On SIGTERM the gateway stops accepting connections, waits up to 30 s for in-flight requests, then closes remaining connections and flushes pending usage writes (15 s). Give the container at least 50 s (`stop_grace_period: 60s` in Compose, `terminationGracePeriodSeconds` in Kubernetes). Streams longer than the grace period are cut; their reservations are settled when recorded, or released by the sweeper after `RESERVATION_TTL`.

## Health checks

- **Liveness:** `GET /healthz`, or `/api healthcheck` inside the container (no shell or curl in the image).
- **Readiness:** `GET /readyz` returns 503 when Postgres or Redis is unreachable (Redis is ignored when `RATE_LIMIT_FAIL_OPEN=true`). Route traffic only to ready instances.

## Observability

- **Prometheus** scrapes `api:8080/metrics` (see `deploy/prometheus/prometheus.yml`). Metric labels are bounded: provider slug, model name, status and error code. No team IDs, request IDs or keys are used as labels.
- **Grafana** auto-provisions the "LLM Gateway" dashboard from `deploy/grafana/`: throughput, latency percentiles, errors by code, rate limiting, provider outcomes and latency, circuit states, fallbacks, stream time-to-first-byte, tokens, and Go runtime.
- **Tracing:** set `OTEL_EXPORTER_OTLP_ENDPOINT` to an OTLP/HTTP collector (`host:4318`). Spans cover the HTTP request, rate limiting, budget reservation, each provider attempt (with outbound HTTP), Postgres queries and Redis commands. Usage recording runs after the response and appears as a separate `gateway.record` trace.
- **Logs:** JSON on stdout, one access line per request with `request_id`, `team_id`, status and latency. Prompts, completions, keys and credentials are never logged.

Suggested alerts:

| Alert | Expression |
|---|---|
| Circuit open | `max by (provider) (llm_gateway_circuit_breaker_state) == 2` for 5m |
| High error ratio | `sum(rate(llm_gateway_requests_total{status="error"}[5m])) / sum(rate(llm_gateway_requests_total[5m])) > 0.05` |
| Usage writes failing | `rate(llm_gateway_usage_record_failures_total[5m]) > 0` |
| Redis degraded | `rate(llm_gateway_rate_limited_total{reason="degraded_fail_open"}[5m]) > 0` |

## Upgrades

1. Build and push new images.
2. Roll out the gateway first; it applies new migrations on start (forward-only, additive by convention).
3. Roll out the console.

## Kubernetes (outline)

No manifests ship with the MVP. A typical layout: a `Deployment` for the gateway (2+ replicas, readiness `/readyz`, liveness `/healthz`, grace period 60 s), a `Deployment` for the console with `BACKEND_URL=http://gateway:8080`, managed Postgres and Redis, an Ingress with SSE-friendly settings (`proxy-buffering: off`, read timeout ≥ 15 m), and a `ServiceMonitor` for `/metrics`.
