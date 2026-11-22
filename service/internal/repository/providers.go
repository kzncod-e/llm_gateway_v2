package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"service/internal/model"
)

const providerCols = `id, slug, name, type, base_url, api_key_ciphertext, api_key_last4, key_version, enabled, priority, timeout_ms, created_at, updated_at`

func scanProvider(row pgx.Row) (*model.Provider, error) {
	var p model.Provider
	if err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Type, &p.BaseURL, &p.APIKeyCiphertext, &p.APIKeyLast4, &p.KeyVersion,
		&p.Enabled, &p.Priority, &p.TimeoutMs, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	p.HasAPIKey = len(p.APIKeyCiphertext) > 0
	return &p, nil
}

func (r *Repo) CreateProvider(ctx context.Context, p *model.Provider) error {
	return mapErr(r.db.QueryRow(ctx, `INSERT INTO providers (id, slug, name, type, base_url, api_key_ciphertext, api_key_last4, enabled, priority, timeout_ms)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING created_at, updated_at`,
		p.ID, p.Slug, p.Name, p.Type, p.BaseURL, p.APIKeyCiphertext, p.APIKeyLast4, p.Enabled, p.Priority, p.TimeoutMs).Scan(&p.CreatedAt, &p.UpdatedAt))
}

func (r *Repo) Provider(ctx context.Context, id uuid.UUID) (*model.Provider, error) {
	return scanProvider(r.db.QueryRow(ctx, `SELECT `+providerCols+` FROM providers WHERE id = $1`, id))
}

func (r *Repo) ListProviders(ctx context.Context) ([]*model.Provider, error) {
	rows, err := r.db.Query(ctx, `SELECT `+providerCols+` FROM providers ORDER BY priority, slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.Provider{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type ProviderUpdate struct {
	Name      *string
	BaseURL   *string
	Enabled   *bool
	Priority  *int
	TimeoutMs *int
}

func (r *Repo) UpdateProvider(ctx context.Context, id uuid.UUID, up ProviderUpdate) error {
	return affected(r.db.Exec(ctx, `UPDATE providers SET name = COALESCE($2, name), base_url = COALESCE($3, base_url),
		enabled = COALESCE($4, enabled), priority = COALESCE($5, priority), timeout_ms = COALESCE($6, timeout_ms), updated_at = now()
		WHERE id = $1`, id, up.Name, up.BaseURL, up.Enabled, up.Priority, up.TimeoutMs))
}

func (r *Repo) SetProviderCredential(ctx context.Context, id uuid.UUID, ciphertext []byte, last4 string) error {
	return affected(r.db.Exec(ctx, `UPDATE providers SET api_key_ciphertext = $2, api_key_last4 = $3, key_version = key_version + 1, updated_at = now()
		WHERE id = $1`, id, ciphertext, last4))
}

func (r *Repo) DeleteProvider(ctx context.Context, id uuid.UUID) error {
	return affected(r.db.Exec(ctx, `DELETE FROM providers WHERE id = $1`, id))
}

// Models -------------------------------------------------------------------------------

func (r *Repo) CreateModel(ctx context.Context, m *model.Model) error {
	return mapErr(r.db.QueryRow(ctx, `INSERT INTO models (id, provider_id, name, display_name, enabled) VALUES ($1,$2,$3,$4,$5)
		RETURNING created_at, updated_at`, m.ID, m.ProviderID, m.Name, m.DisplayName, m.Enabled).Scan(&m.CreatedAt, &m.UpdatedAt))
}

type ModelUpdate struct {
	DisplayName *string
	Enabled     *bool
}

func (r *Repo) UpdateModel(ctx context.Context, id uuid.UUID, up ModelUpdate) error {
	return affected(r.db.Exec(ctx, `UPDATE models SET display_name = COALESCE($2, display_name), enabled = COALESCE($3, enabled), updated_at = now()
		WHERE id = $1`, id, up.DisplayName, up.Enabled))
}

func (r *Repo) DeleteModel(ctx context.Context, id uuid.UUID) error {
	return affected(r.db.Exec(ctx, `DELETE FROM models WHERE id = $1`, id))
}

// ListModels returns models (optionally for one provider) with their current price.
func (r *Repo) ListModels(ctx context.Context, providerID *uuid.UUID) ([]*model.Model, error) {
	rows, err := r.db.Query(ctx, `SELECT m.id, m.provider_id, p.slug, m.name, m.display_name, m.enabled, m.created_at, m.updated_at,
		mp.id, mp.effective_from, mp.currency, mp.input_per_mtok_nanos, mp.cached_input_per_mtok_nanos, mp.cache_write_per_mtok_nanos, mp.output_per_mtok_nanos, mp.created_at
		FROM models m JOIN providers p ON p.id = m.provider_id
		LEFT JOIN LATERAL (SELECT * FROM model_pricing x WHERE x.model_id = m.id AND x.effective_from <= now() ORDER BY x.effective_from DESC LIMIT 1) mp ON TRUE
		WHERE ($1::uuid IS NULL OR m.provider_id = $1)
		ORDER BY p.priority, p.slug, m.name`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.Model{}
	for rows.Next() {
		var m model.Model
		var pid *uuid.UUID
		var eff, created *time.Time
		var cur *string
		var in, cin, cw, outp *int64
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.ProviderSlug, &m.Name, &m.DisplayName, &m.Enabled, &m.CreatedAt, &m.UpdatedAt,
			&pid, &eff, &cur, &in, &cin, &cw, &outp, &created); err != nil {
			return nil, err
		}
		if pid != nil {
			m.CurrentPrice = &model.ModelPrice{ID: *pid, ModelID: m.ID, EffectiveFrom: *eff, Currency: *cur, InputPerM: *in, CachedInputPerM: *cin,
				CacheWritePerM: *cw, OutputPerM: *outp, CreatedAt: *created}
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

func (r *Repo) Model(ctx context.Context, id uuid.UUID) (*model.Model, error) {
	var m model.Model
	err := r.db.QueryRow(ctx, `SELECT m.id, m.provider_id, p.slug, m.name, m.display_name, m.enabled, m.created_at, m.updated_at
		FROM models m JOIN providers p ON p.id = m.provider_id WHERE m.id = $1`, id).
		Scan(&m.ID, &m.ProviderID, &m.ProviderSlug, &m.Name, &m.DisplayName, &m.Enabled, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &m, nil
}

// Pricing ------------------------------------------------------------------------------

func (r *Repo) AddPrice(ctx context.Context, p *model.ModelPrice, createdBy *uuid.UUID) error {
	return mapErr(r.db.QueryRow(ctx, `INSERT INTO model_pricing (id, model_id, effective_from, currency, input_per_mtok_nanos, cached_input_per_mtok_nanos,
		cache_write_per_mtok_nanos, output_per_mtok_nanos, created_by) VALUES ($1,$2,$3,'USD',$4,$5,$6,$7,$8) RETURNING created_at, currency`,
		p.ID, p.ModelID, p.EffectiveFrom, p.InputPerM, p.CachedInputPerM, p.CacheWritePerM, p.OutputPerM, createdBy).Scan(&p.CreatedAt, &p.Currency))
}

func (r *Repo) ListPrices(ctx context.Context, modelID uuid.UUID) ([]*model.ModelPrice, error) {
	rows, err := r.db.Query(ctx, `SELECT id, model_id, effective_from, currency, input_per_mtok_nanos, cached_input_per_mtok_nanos, cache_write_per_mtok_nanos,
		output_per_mtok_nanos, created_at FROM model_pricing WHERE model_id = $1 ORDER BY effective_from DESC`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.ModelPrice{}
	for rows.Next() {
		var p model.ModelPrice
		if err := rows.Scan(&p.ID, &p.ModelID, &p.EffectiveFrom, &p.Currency, &p.InputPerM, &p.CachedInputPerM, &p.CacheWritePerM, &p.OutputPerM, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// Routing policies -------------------------------------------------------------------

func (r *Repo) ListPolicies(ctx context.Context) ([]*model.RoutingPolicy, error) {
	rows, err := r.db.Query(ctx, `SELECT p.id, p.alias, p.description, p.max_attempts_per_target, p.retry_base_ms, p.retry_max_ms, p.enabled, p.created_at, p.updated_at,
		COALESCE(array_agg(t.model_id ORDER BY t.position) FILTER (WHERE t.model_id IS NOT NULL), '{}')
		FROM routing_policies p LEFT JOIN routing_policy_targets t ON t.policy_id = p.id
		GROUP BY p.id ORDER BY p.alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.RoutingPolicy{}
	for rows.Next() {
		var p model.RoutingPolicy
		if err := rows.Scan(&p.ID, &p.Alias, &p.Description, &p.MaxAttemptsPerTarget, &p.RetryBaseMs, &p.RetryMaxMs, &p.Enabled, &p.CreatedAt, &p.UpdatedAt, &p.TargetModelIDs); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// SavePolicy inserts or updates a policy and replaces its targets atomically.
func (r *Repo) SavePolicy(ctx context.Context, p *model.RoutingPolicy, create bool) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if create {
		err = tx.QueryRow(ctx, `INSERT INTO routing_policies (id, alias, description, max_attempts_per_target, retry_base_ms, retry_max_ms, enabled)
			VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at, updated_at`, p.ID, p.Alias, p.Description, p.MaxAttemptsPerTarget, p.RetryBaseMs, p.RetryMaxMs, p.Enabled).
			Scan(&p.CreatedAt, &p.UpdatedAt)
	} else {
		err = tx.QueryRow(ctx, `UPDATE routing_policies SET alias = $2, description = $3, max_attempts_per_target = $4, retry_base_ms = $5, retry_max_ms = $6,
			enabled = $7, updated_at = now() WHERE id = $1 RETURNING created_at, updated_at`, p.ID, p.Alias, p.Description, p.MaxAttemptsPerTarget, p.RetryBaseMs, p.RetryMaxMs, p.Enabled).
			Scan(&p.CreatedAt, &p.UpdatedAt)
	}
	if err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM routing_policy_targets WHERE policy_id = $1`, p.ID); err != nil {
		return err
	}
	for i, mid := range p.TargetModelIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO routing_policy_targets (policy_id, position, model_id) VALUES ($1,$2,$3)`, p.ID, i, mid); err != nil {
			return mapErr(err)
		}
	}
	return tx.Commit(ctx)
}

func (r *Repo) DeletePolicy(ctx context.Context, id uuid.UUID) error {
	return affected(r.db.Exec(ctx, `DELETE FROM routing_policies WHERE id = $1`, id))
}
