# API Reference

Two APIs are served by the gateway on the same port (default `:8080`):

- **Data plane** `/v1/*`: for applications. Authenticated with a gateway API key.
- **Control plane** `/api/v1/*`: for the console. Authenticated with the `gw_session` cookie.

All money values are **integer nano-USD** (1 USD = 1,000,000,000). All timestamps are RFC 3339 UTC.

## Errors

Both planes return the same envelope:

```json
{ "error": { "type": "invalid_request_error", "code": "model_not_found", "message": "model \"x\" is not available" } }
```

| HTTP | `code` | Meaning |
|---|---|---|
| 400 | `invalid_request` | Malformed JSON, validation failure, or the provider rejected the request |
| 401 | `invalid_api_key` / `unauthorized` / `invalid_credentials` | Missing, invalid, revoked or expired key; no session; wrong password |
| 402 | `budget_exceeded` | Team monthly or daily budget cannot cover the request's worst-case cost |
| 403 | `forbidden` | Authenticated but not allowed |
| 404 | `model_not_found` / `not_found` | Unknown model or route; team-scoped resources outside your teams |
| 409 | `conflict` | Duplicate slug, alias, email or model name |
| 413 | `request_too_large` | Body over 1 MiB (data plane, playground) or 256 KiB (control plane) |
| 415 | `unsupported_media_type` | Control-plane mutation without `Content-Type: application/json` |
| 429 | `rate_limit_exceeded` / `concurrency_limit_exceeded` / `upstream_rate_limited` / `too_many_attempts` | Team limit, all providers rate limiting, or login throttling. `Retry-After` is set |
| 502 | `upstream_error` | All targets failed |
| 503 | `no_healthy_provider` / `pricing_not_configured` / `service_unavailable` | All circuits open; budgeted team routed to unpriced models; Postgres or Redis down |
| 504 | `upstream_timeout` | Provider timed out on the final attempt |

Every response carries `X-Request-ID`.

---

## Data plane

Header: `Authorization: Bearer gwk_...`

### `POST /v1/chat/completions`

OpenAI Chat Completions compatible.

| Field | Type | Notes |
|---|---|---|
| `model` | string | Routing policy alias, `provider-slug/model`, or a model name |
| `messages` | array | `role`: `system`, `developer` (treated as system), `user`, `assistant`. `content`: string or array of `{"type":"text","text":...}` |
| `stream` | bool | SSE when true |
| `stream_options.include_usage` | bool | Emit a final chunk with `usage` before `[DONE]` |
| `temperature` | number | 0–2 |
| `top_p` | number | 0–1 |
| `max_tokens` / `max_completion_tokens` | int | 1–200000. Defaults to `DEFAULT_MAX_OUTPUT_TOKENS`, which also bounds the budget reservation |
| `stop` | string or string[] | At most 4 |
| `user` | string | Forwarded where supported |

Not supported (returns 400): `tools`, `functions`, `tool_choice`, `n` other than 1, non-text content parts.

Optional header `X-Gateway-Provider: <slug>` forces a provider for a plain model name.

**Response headers:** `X-Request-ID`, `X-Gateway-Provider`, `X-Gateway-Model`, `X-Gateway-Attempts`.

**Non-streaming response**

```json
{
  "id": "chatcmpl-01a2044a-2858-7620-b447-35ef8c3aae07",
  "object": "chat.completion",
  "created": 1795368298,
  "model": "claude-haiku-4-5",
  "choices": [{ "index": 0, "message": { "role": "assistant", "content": "..." }, "finish_reason": "stop" }],
  "usage": {
    "prompt_tokens": 12, "completion_tokens": 54, "total_tokens": 66,
    "prompt_tokens_details": { "cached_tokens": 0 },
    "completion_tokens_details": { "reasoning_tokens": 0 },
    "cost_nanos": 271000
  }
}
```

`usage.cost_nanos` is a gateway extension: the cost computed from the price version in effect. It is omitted when the model has no price. `prompt_tokens` includes cached and cache-write tokens, as in OpenAI's format. `usage` is omitted if the provider did not report usage.

**Streaming response**

```
data: {"id":"chatcmpl-…","object":"chat.completion.chunk","created":…,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}
data: {"id":"chatcmpl-…","object":"chat.completion.chunk",…,"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}
data: {"id":"chatcmpl-…","object":"chat.completion.chunk",…,"choices":[],"usage":{…}}
data: [DONE]
```

If the provider fails before the first chunk, the gateway retries or falls back invisibly; if everything fails, a normal JSON error is returned (no SSE). If the provider fails after the first chunk, the stream ends with an error event and **no** `[DONE]`:

```
data: {"error":{"type":"server_error","code":"stream_interrupted","message":"the upstream provider stream was interrupted: stream_interrupted"}}
```

### `GET /v1/models`

```json
{ "object": "list", "data": [{ "id": "smart", "object": "model", "created": 0, "owned_by": "llm-gateway" }, …] }
```

Lists enabled policy aliases, model names and `provider-slug/model` identifiers.

---

## Control plane

Cookie `gw_session` (HttpOnly). Mutating requests must send `Content-Type: application/json`. Lists return `{ "data": [...] }`.

Roles: platform `admin` (everything) and `member`. Team roles: `owner` ⊃ `developer` ⊃ `viewer`. "Viewer+" means any member of that team.

### Auth

| Method & path | Who | Body / notes |
|---|---|---|
| `POST /api/v1/auth/login` | anyone | `{email, password}` → `{user}` and sets the cookie. Throttled to 10 attempts per minute per IP and email |
| `POST /api/v1/auth/logout` | session | Deletes the session |
| `GET /api/v1/auth/me` | session | `{user, teams: [{…team, role}]}` |
| `PATCH /api/v1/auth/me` | session | `{name}` |
| `POST /api/v1/auth/password` | session | `{current_password, new_password}` (min 12 chars) |

### Users (admin)

| Method & path | Body |
|---|---|
| `GET /api/v1/users` | |
| `POST /api/v1/users` | `{email, name, password, role: "admin"|"member"}` |
| `PATCH /api/v1/users/:id` | `{name?, role?, disabled?}`. Disabling deletes the user's sessions |

### Teams

| Method & path | Who | Body / response |
|---|---|---|
| `GET /api/v1/teams` | session | Teams visible to you |
| `POST /api/v1/teams` | admin | `{name, slug?, description?, rpm_limit?, concurrency_limit?}` |
| `GET /api/v1/teams/:id` | viewer+ | `{team, role}` |
| `PATCH /api/v1/teams/:id` | owner (name, description); admin (limits, archive) | `{name?, description?, rpm_limit?, concurrency_limit?, clear_rpm_limit?, clear_concurrency_limit?, archived?}` |
| `GET /api/v1/teams/:id/members` | viewer+ | |
| `POST /api/v1/teams/:id/members` | owner | `{email, role}` (upsert; the user must exist) |
| `DELETE /api/v1/teams/:id/members/:userId` | owner | |
| `GET /api/v1/teams/:id/budget` | viewer+ | Budget status (below) |
| `PUT /api/v1/teams/:id/budget` | admin | `{monthly_limit_nanos: int|null, daily_limit_nanos: int|null, warn_threshold_pct?}` |
| `GET /api/v1/teams/:id/api-keys` | viewer+ | Keys without secrets (`prefix` only) |
| `POST /api/v1/teams/:id/api-keys` | developer+ | `{name, expires_in_days?}` → key object plus `"key": "gwk_…"`, **returned only here** |
| `DELETE /api/v1/teams/:id/api-keys/:keyId` | developer+ | Revokes immediately on this instance, within 30 s on others |

Budget status:

```json
{ "team_id": "…", "monthly_limit_nanos": 25000000000, "daily_limit_nanos": null, "warn_threshold_pct": 80,
  "month_spent_nanos": 2203000, "month_reserved_nanos": 0, "day_spent_nanos": 2203000, "day_reserved_nanos": 0,
  "month_remaining_nanos": 24997797000, "day_remaining_nanos": null, "warning": false, "exhausted": false, "period_month": "2026-11" }
```

### Providers, models, pricing, routing

Reads are open to any session; writes are admin-only. Provider credentials are write-only.

| Method & path | Body / notes |
|---|---|
| `GET /api/v1/providers` | Includes `circuit_state`, `has_api_key`, `api_key_last4` |
| `POST /api/v1/providers` | `{name, type: openai|anthropic|gemini, slug?, base_url?, api_key?, enabled?, priority?, timeout_ms?}` |
| `GET /api/v1/providers/:id` | `{provider, circuit}` |
| `PATCH /api/v1/providers/:id` | `{name?, base_url?, enabled?, priority?, timeout_ms?, api_key?}`. Sending `api_key` rotates the credential |
| `DELETE /api/v1/providers/:id` | Deletes its models and prices; request history is kept |
| `GET /api/v1/models?provider_id=` | Includes `current_price` |
| `POST /api/v1/models` | `{provider_id, name, display_name?, enabled?}` |
| `PATCH /api/v1/models/:id` | `{display_name?, enabled?}` |
| `DELETE /api/v1/models/:id` | |
| `GET /api/v1/models/:id/prices` | All versions, newest first |
| `POST /api/v1/models/:id/prices` | `{input_per_mtok_nanos, output_per_mtok_nanos, cached_input_per_mtok_nanos?, cache_write_per_mtok_nanos?, effective_from?}`. Unset cache prices default to the input price |
| `GET /api/v1/routing-policies` | |
| `POST /api/v1/routing-policies` | `{alias, description?, target_model_ids: [ordered], max_attempts_per_target?, retry_base_ms?, retry_max_ms?, enabled?}` |
| `PUT /api/v1/routing-policies/:id` | Same body; replaces targets |
| `DELETE /api/v1/routing-policies/:id` | |

### Analytics and logs

Team isolation applies: non-admins only see teams they belong to.

| Method & path | Query |
|---|---|
| `GET /api/v1/analytics/summary` | `from`, `to` (RFC 3339 or `YYYY-MM-DD`; default last 7 days; max 400 days), `team_id?` |
| `GET /api/v1/analytics/timeseries` | Same, plus `interval=hour|day` (hourly ranges up to 31 days). Empty buckets are zero-filled |
| `GET /api/v1/analytics/breakdown` | Same, plus `by=provider|model|team` |
| `GET /api/v1/requests` | `status`, `team_id`, `provider_id`, `source=api|playground`, `model` (substring), `q` (request ID or model), `from`, `to`, `limit` (≤200), `cursor` → `{data, next_cursor}` |
| `GET /api/v1/requests/:id` | Request plus `attempts[]` with per-attempt usage and cost |
| `GET /api/v1/reliability` | `window=1h|24h` → providers with circuit and stats, hourly series, recent failures, summary |
| `POST /api/v1/reliability/breakers/:providerId/reset` | admin; forces the circuit closed on this instance |
| `GET /api/v1/audit-logs` | admin; `limit`, `offset` |
| `GET /api/v1/settings` | Non-secret runtime configuration |

### Playground

`POST /api/v1/playground/chat?team_id=<uuid>`, developer+. Same body and responses as `/v1/chat/completions`. Runs through the gateway engine with the team's limits and budget; recorded with `source = playground`.

---

## Operational endpoints

| Path | Purpose |
|---|---|
| `GET /healthz` | Liveness (process up) |
| `GET /readyz` | Readiness: Postgres and Redis reachable (Redis ignored when `RATE_LIMIT_FAIL_OPEN=true`) |
| `GET /metrics` | Prometheus metrics. Restrict access at the proxy in production |
