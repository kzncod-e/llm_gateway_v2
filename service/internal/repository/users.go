package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"service/internal/model"
)

const userCols = `id, email, name, role, password_hash, disabled_at, last_login_at, created_at`

func scanUser(row pgx.Row) (*model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.PasswordHash, &u.DisabledAt, &u.LastLoginAt, &u.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

func (r *Repo) CreateUser(ctx context.Context, u *model.User) error {
	return mapErr(r.db.QueryRow(ctx, `INSERT INTO users (id, email, name, role, password_hash) VALUES ($1, lower($2), $3, $4, $5) RETURNING created_at`,
		u.ID, u.Email, u.Name, u.Role, u.PasswordHash).Scan(&u.CreatedAt))
}

func (r *Repo) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (r *Repo) UserByEmail(ctx context.Context, email string) (*model.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(email) = lower($1)`, email))
}

func (r *Repo) UserByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (r *Repo) ListUsers(ctx context.Context) ([]*model.User, error) {
	rows, err := r.db.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type UserUpdate struct {
	Name     *string
	Role     *string
	Disabled *bool
}

func (r *Repo) UpdateUser(ctx context.Context, id uuid.UUID, up UserUpdate) error {
	var disabledAt any = nil
	setDisabled := up.Disabled != nil
	if setDisabled && *up.Disabled {
		disabledAt = time.Now()
	}
	return affected(r.db.Exec(ctx, `UPDATE users SET
		name = COALESCE($2, name),
		role = COALESCE($3, role),
		disabled_at = CASE WHEN $4 THEN $5::timestamptz ELSE disabled_at END,
		updated_at = now()
		WHERE id = $1`, id, up.Name, up.Role, setDisabled, disabledAt))
}

func (r *Repo) SetPassword(ctx context.Context, id uuid.UUID, hash string) error {
	return affected(r.db.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash))
}

func (r *Repo) TouchLogin(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, id)
	return err
}

// Sessions ------------------------------------------------------------------------

func (r *Repo) CreateSession(ctx context.Context, userID uuid.UUID, tokenHash []byte, ua, ip string, expires time.Time) error {
	_, err := r.db.Exec(ctx, `INSERT INTO user_sessions (id, user_id, token_hash, user_agent, ip, expires_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		model.NewID(), userID, tokenHash, truncate(ua, 300), ip, expires)
	return mapErr(err)
}

// SessionUser returns the active user owning a session token hash.
func (r *Repo) SessionUser(ctx context.Context, tokenHash []byte) (*model.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT u.id, u.email, u.name, u.role, u.password_hash, u.disabled_at, u.last_login_at, u.created_at
		FROM user_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND u.disabled_at IS NULL`, tokenHash))
}

func (r *Repo) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := r.db.Exec(ctx, `DELETE FROM user_sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func (r *Repo) DeleteUserSessions(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM user_sessions WHERE user_id = $1`, userID)
	return err
}

func (r *Repo) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM user_sessions WHERE expires_at < now()`)
	return tag.RowsAffected(), err
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
