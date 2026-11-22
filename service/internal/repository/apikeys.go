package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"service/internal/model"
)

const keyCols = `id, team_id, name, prefix, created_by, expires_at, revoked_at, last_used_at, created_at`

func scanKey(row pgx.Row) (*model.APIKey, error) {
	var k model.APIKey
	if err := row.Scan(&k.ID, &k.TeamID, &k.Name, &k.Prefix, &k.CreatedBy, &k.ExpiresAt, &k.RevokedAt, &k.LastUsedAt, &k.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &k, nil
}

func (r *Repo) CreateAPIKey(ctx context.Context, k *model.APIKey, hash []byte) error {
	return mapErr(r.db.QueryRow(ctx, `INSERT INTO api_keys (id, team_id, name, prefix, key_hash, created_by, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, k.ID, k.TeamID, k.Name, k.Prefix, hash, k.CreatedBy, k.ExpiresAt).Scan(&k.CreatedAt))
}

func (r *Repo) ListAPIKeys(ctx context.Context, teamID uuid.UUID) ([]*model.APIKey, error) {
	rows, err := r.db.Query(ctx, `SELECT `+keyCols+` FROM api_keys WHERE team_id = $1 ORDER BY created_at DESC`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.APIKey{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (r *Repo) RevokeAPIKey(ctx context.Context, teamID, keyID uuid.UUID) error {
	return affected(r.db.Exec(ctx, `UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND team_id = $2 AND revoked_at IS NULL`, keyID, teamID))
}

// KeyPrincipal is what the data plane needs to authorize a request.
type KeyPrincipal struct {
	Key  model.APIKey
	Team model.Team
}

func (r *Repo) KeyByHash(ctx context.Context, hash []byte) (*KeyPrincipal, error) {
	var p KeyPrincipal
	k, t := &p.Key, &p.Team
	err := r.db.QueryRow(ctx, `SELECT k.id, k.team_id, k.name, k.prefix, k.created_by, k.expires_at, k.revoked_at, k.last_used_at, k.created_at,
		t.id, t.name, t.slug, t.description, t.rpm_limit, t.concurrency_limit, t.archived_at, t.created_at, t.updated_at
		FROM api_keys k JOIN teams t ON t.id = k.team_id WHERE k.key_hash = $1`, hash).
		Scan(&k.ID, &k.TeamID, &k.Name, &k.Prefix, &k.CreatedBy, &k.ExpiresAt, &k.RevokedAt, &k.LastUsedAt, &k.CreatedAt,
			&t.ID, &t.Name, &t.Slug, &t.Description, &t.RPMLimit, &t.ConcurrencyLimit, &t.ArchivedAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &p, nil
}

func (r *Repo) TouchAPIKey(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE api_keys SET last_used_at = $2 WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < $2)`, id, at)
	return err
}
