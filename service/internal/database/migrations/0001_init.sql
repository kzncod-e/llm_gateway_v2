-- Initial schema for the LLM gateway.
-- Money columns are BIGINT nano-USD (1 USD = 1e9). Prices are per 1M tokens in nanos.

CREATE TABLE users (
    id             UUID PRIMARY KEY,
    email          TEXT NOT NULL,
    name           TEXT NOT NULL DEFAULT '',
    password_hash  TEXT NOT NULL,
    role           TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    disabled_at    TIMESTAMPTZ,
    last_login_at  TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

CREATE TABLE user_sessions (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   BYTEA NOT NULL UNIQUE,
    user_agent   TEXT NOT NULL DEFAULT '',
    ip           TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX user_sessions_user_idx ON user_sessions (user_id);
CREATE INDEX user_sessions_expires_idx ON user_sessions (expires_at);

CREATE TABLE teams (
    id                 UUID PRIMARY KEY,
    name               TEXT NOT NULL,
    slug               TEXT NOT NULL UNIQUE,
    description        TEXT NOT NULL DEFAULT '',
    rpm_limit          INTEGER CHECK (rpm_limit > 0),
    concurrency_limit  INTEGER CHECK (concurrency_limit > 0),
    archived_at        TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE team_members (
    team_id     UUID NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role        TEXT NOT NULL CHECK (role IN ('owner', 'developer', 'viewer')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id)
);
CREATE INDEX team_members_user_idx ON team_members (user_id);

CREATE TABLE api_keys (
    id            UUID PRIMARY KEY,
    team_id       UUID NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    prefix        TEXT NOT NULL,
    key_hash      BYTEA NOT NULL UNIQUE,
    created_by    UUID REFERENCES users (id) ON DELETE SET NULL,
    expires_at    TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ,
    last_used_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX api_keys_team_idx ON api_keys (team_id, created_at DESC);

CREATE TABLE providers (
    id                  UUID PRIMARY KEY,
    slug                TEXT NOT NULL UNIQUE,
    name                TEXT NOT NULL,
    type                TEXT NOT NULL CHECK (type IN ('openai', 'anthropic', 'gemini')),
    base_url            TEXT NOT NULL,
    api_key_ciphertext  BYTEA,
    api_key_last4       TEXT NOT NULL DEFAULT '',
    key_version         INTEGER NOT NULL DEFAULT 1,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    priority            INTEGER NOT NULL DEFAULT 100,
    timeout_ms          INTEGER NOT NULL DEFAULT 60000 CHECK (timeout_ms BETWEEN 1000 AND 600000),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE models (
    id            UUID PRIMARY KEY,
    provider_id   UUID NOT NULL REFERENCES providers (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    display_name  TEXT NOT NULL DEFAULT '',
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider_id, name)
);
CREATE INDEX models_name_idx ON models (name);

CREATE TABLE model_pricing (
    id                           UUID PRIMARY KEY,
    model_id                     UUID NOT NULL REFERENCES models (id) ON DELETE CASCADE,
    effective_from               TIMESTAMPTZ NOT NULL,
    currency                     TEXT NOT NULL DEFAULT 'USD' CHECK (currency = 'USD'),
    input_per_mtok_nanos         BIGINT NOT NULL CHECK (input_per_mtok_nanos >= 0),
    cached_input_per_mtok_nanos  BIGINT NOT NULL CHECK (cached_input_per_mtok_nanos >= 0),
    cache_write_per_mtok_nanos   BIGINT NOT NULL CHECK (cache_write_per_mtok_nanos >= 0),
    output_per_mtok_nanos        BIGINT NOT NULL CHECK (output_per_mtok_nanos >= 0),
    created_by                   UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at                   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (model_id, effective_from)
);

CREATE TABLE routing_policies (
    id                       UUID PRIMARY KEY,
    alias                    TEXT NOT NULL UNIQUE,
    description              TEXT NOT NULL DEFAULT '',
    max_attempts_per_target  INTEGER NOT NULL DEFAULT 2 CHECK (max_attempts_per_target BETWEEN 1 AND 5),
    retry_base_ms            INTEGER NOT NULL DEFAULT 250 CHECK (retry_base_ms BETWEEN 0 AND 10000),
    retry_max_ms             INTEGER NOT NULL DEFAULT 2000 CHECK (retry_max_ms BETWEEN 0 AND 30000),
    enabled                  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE routing_policy_targets (
    policy_id  UUID NOT NULL REFERENCES routing_policies (id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    model_id   UUID NOT NULL REFERENCES models (id) ON DELETE CASCADE,
    PRIMARY KEY (policy_id, position)
);

CREATE TABLE team_budgets (
    team_id              UUID PRIMARY KEY REFERENCES teams (id) ON DELETE CASCADE,
    monthly_limit_nanos  BIGINT CHECK (monthly_limit_nanos >= 0),
    daily_limit_nanos    BIGINT CHECK (daily_limit_nanos >= 0),
    warn_threshold_pct   INTEGER NOT NULL DEFAULT 80 CHECK (warn_threshold_pct BETWEEN 1 AND 100),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Running counters per team and period (UTC calendar month / day).
CREATE TABLE budget_periods (
    team_id         UUID NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    period_type     TEXT NOT NULL CHECK (period_type IN ('month', 'day')),
    period_start    DATE NOT NULL,
    spent_nanos     BIGINT NOT NULL DEFAULT 0 CHECK (spent_nanos >= 0),
    reserved_nanos  BIGINT NOT NULL DEFAULT 0 CHECK (reserved_nanos >= 0),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, period_type, period_start)
);

CREATE TABLE budget_reservations (
    id            UUID PRIMARY KEY,
    team_id       UUID NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    request_id    UUID NOT NULL,
    amount_nanos  BIGINT NOT NULL CHECK (amount_nanos >= 0),
    month_start   DATE NOT NULL,
    day_start     DATE NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('active', 'settled', 'released')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    finalized_at  TIMESTAMPTZ
);
CREATE INDEX budget_reservations_active_idx ON budget_reservations (expires_at) WHERE status = 'active';

CREATE TABLE gateway_requests (
    id                   UUID PRIMARY KEY,
    team_id              UUID NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    api_key_id           UUID REFERENCES api_keys (id) ON DELETE SET NULL,
    user_id              UUID REFERENCES users (id) ON DELETE SET NULL,
    source               TEXT NOT NULL CHECK (source IN ('api', 'playground')),
    requested_model      TEXT NOT NULL,
    stream               BOOLEAN NOT NULL,
    status               TEXT NOT NULL CHECK (status IN ('success', 'error', 'interrupted', 'client_cancelled', 'rejected')),
    http_status          INTEGER NOT NULL,
    error_code           TEXT NOT NULL DEFAULT '',
    error_message        TEXT NOT NULL DEFAULT '',
    final_provider_id    UUID REFERENCES providers (id) ON DELETE SET NULL,
    final_model_id       UUID REFERENCES models (id) ON DELETE SET NULL,
    attempt_count        INTEGER NOT NULL DEFAULT 0,
    fallback_count       INTEGER NOT NULL DEFAULT 0,
    input_tokens         BIGINT,
    output_tokens        BIGINT,
    total_tokens         BIGINT,
    usage_status         TEXT NOT NULL CHECK (usage_status IN ('reported', 'unknown', 'none')),
    cost_nanos           BIGINT,
    cost_status          TEXT NOT NULL CHECK (cost_status IN ('exact', 'estimated', 'unknown', 'none')),
    budget_charge_nanos  BIGINT NOT NULL DEFAULT 0,
    latency_ms           INTEGER NOT NULL,
    ttfb_ms              INTEGER,
    created_at           TIMESTAMPTZ NOT NULL,
    completed_at         TIMESTAMPTZ NOT NULL
);
CREATE INDEX gateway_requests_created_idx ON gateway_requests (created_at DESC, id DESC);
CREATE INDEX gateway_requests_team_created_idx ON gateway_requests (team_id, created_at DESC, id DESC);
CREATE INDEX gateway_requests_status_created_idx ON gateway_requests (status, created_at DESC);

CREATE TABLE provider_attempts (
    id              UUID PRIMARY KEY,
    request_id      UUID NOT NULL REFERENCES gateway_requests (id) ON DELETE CASCADE,
    attempt_number  INTEGER NOT NULL,
    provider_id     UUID REFERENCES providers (id) ON DELETE SET NULL,
    model_id        UUID REFERENCES models (id) ON DELETE SET NULL,
    provider_slug   TEXT NOT NULL,
    model_name      TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('success', 'error', 'interrupted', 'client_cancelled', 'skipped')),
    http_status     INTEGER,
    error_kind      TEXT NOT NULL DEFAULT '',
    error_message   TEXT NOT NULL DEFAULT '',
    retryable       BOOLEAN NOT NULL DEFAULT FALSE,
    started_at      TIMESTAMPTZ NOT NULL,
    duration_ms     INTEGER NOT NULL,
    ttfb_ms         INTEGER,
    UNIQUE (request_id, attempt_number)
);
CREATE INDEX provider_attempts_provider_started_idx ON provider_attempts (provider_id, started_at DESC);
CREATE INDEX provider_attempts_started_idx ON provider_attempts (started_at DESC);

-- Financial ledger: one row per attempt that reached a provider.
CREATE TABLE usage_records (
    id                   UUID PRIMARY KEY,
    attempt_id           UUID NOT NULL UNIQUE REFERENCES provider_attempts (id) ON DELETE CASCADE,
    request_id           UUID NOT NULL REFERENCES gateway_requests (id) ON DELETE CASCADE,
    team_id              UUID NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    api_key_id           UUID REFERENCES api_keys (id) ON DELETE SET NULL,
    provider_id          UUID REFERENCES providers (id) ON DELETE SET NULL,
    model_id             UUID REFERENCES models (id) ON DELETE SET NULL,
    pricing_id           UUID REFERENCES model_pricing (id) ON DELETE SET NULL,
    usage_status         TEXT NOT NULL CHECK (usage_status IN ('reported', 'unknown', 'none')),
    input_tokens         BIGINT,
    cached_input_tokens  BIGINT,
    cache_write_tokens   BIGINT,
    output_tokens        BIGINT,
    reasoning_tokens     BIGINT,
    total_tokens         BIGINT,
    cost_nanos           BIGINT,
    currency             TEXT NOT NULL DEFAULT 'USD',
    created_at           TIMESTAMPTZ NOT NULL
);
CREATE INDEX usage_records_created_idx ON usage_records (created_at);
CREATE INDEX usage_records_team_created_idx ON usage_records (team_id, created_at);

CREATE TABLE audit_logs (
    id             BIGSERIAL PRIMARY KEY,
    actor_user_id  UUID REFERENCES users (id) ON DELETE SET NULL,
    action         TEXT NOT NULL,
    target_type    TEXT NOT NULL,
    target_id      TEXT NOT NULL DEFAULT '',
    metadata       JSONB NOT NULL DEFAULT '{}'::jsonb,
    ip             TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_created_idx ON audit_logs (created_at DESC);
