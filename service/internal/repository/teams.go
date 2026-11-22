package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"service/internal/model"
)

const teamCols = `id, name, slug, description, rpm_limit, concurrency_limit, archived_at, created_at, updated_at`

func scanTeam(row pgx.Row) (*model.Team, error) {
	var t model.Team
	if err := row.Scan(&t.ID, &t.Name, &t.Slug, &t.Description, &t.RPMLimit, &t.ConcurrencyLimit, &t.ArchivedAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	return &t, nil
}

func (r *Repo) CreateTeam(ctx context.Context, t *model.Team) error {
	return mapErr(r.db.QueryRow(ctx, `INSERT INTO teams (id, name, slug, description, rpm_limit, concurrency_limit)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING created_at, updated_at`,
		t.ID, t.Name, t.Slug, t.Description, t.RPMLimit, t.ConcurrencyLimit).Scan(&t.CreatedAt, &t.UpdatedAt))
}

func (r *Repo) Team(ctx context.Context, id uuid.UUID) (*model.Team, error) {
	return scanTeam(r.db.QueryRow(ctx, `SELECT `+teamCols+` FROM teams WHERE id = $1`, id))
}

// ListTeams returns all teams, or only those in onlyIDs when it is non-nil.
func (r *Repo) ListTeams(ctx context.Context, onlyIDs []uuid.UUID) ([]*model.Team, error) {
	var rows pgx.Rows
	var err error
	if onlyIDs == nil {
		rows, err = r.db.Query(ctx, `SELECT `+teamCols+` FROM teams ORDER BY name`)
	} else {
		rows, err = r.db.Query(ctx, `SELECT `+teamCols+` FROM teams WHERE id = ANY($1) ORDER BY name`, onlyIDs)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.Team{}
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type TeamUpdate struct {
	Name             *string
	Description      *string
	RPMLimit         **int // nil = unchanged, *nil = reset to default
	ConcurrencyLimit **int
	Archived         *bool
}

func (r *Repo) UpdateTeam(ctx context.Context, id uuid.UUID, up TeamUpdate) error {
	setRPM, setConc := up.RPMLimit != nil, up.ConcurrencyLimit != nil
	var rpm, conc *int
	if setRPM {
		rpm = *up.RPMLimit
	}
	if setConc {
		conc = *up.ConcurrencyLimit
	}
	setArch := up.Archived != nil
	var archivedAt *time.Time
	if setArch && *up.Archived {
		now := time.Now()
		archivedAt = &now
	}
	return affected(r.db.Exec(ctx, `UPDATE teams SET
		name = COALESCE($2, name),
		description = COALESCE($3, description),
		rpm_limit = CASE WHEN $4 THEN $5 ELSE rpm_limit END,
		concurrency_limit = CASE WHEN $6 THEN $7 ELSE concurrency_limit END,
		archived_at = CASE WHEN $8 THEN $9 ELSE archived_at END,
		updated_at = now()
		WHERE id = $1`, id, up.Name, up.Description, setRPM, rpm, setConc, conc, setArch, archivedAt))
}

// Members ----------------------------------------------------------------------------

func (r *Repo) UpsertMember(ctx context.Context, teamID, userID uuid.UUID, role string) error {
	_, err := r.db.Exec(ctx, `INSERT INTO team_members (team_id, user_id, role) VALUES ($1,$2,$3)
		ON CONFLICT (team_id, user_id) DO UPDATE SET role = EXCLUDED.role`, teamID, userID, role)
	return mapErr(err)
}

func (r *Repo) RemoveMember(ctx context.Context, teamID, userID uuid.UUID) error {
	return affected(r.db.Exec(ctx, `DELETE FROM team_members WHERE team_id = $1 AND user_id = $2`, teamID, userID))
}

func (r *Repo) ListMembers(ctx context.Context, teamID uuid.UUID) ([]*model.TeamMember, error) {
	rows, err := r.db.Query(ctx, `SELECT m.team_id, m.user_id, u.email, u.name, m.role, m.created_at
		FROM team_members m JOIN users u ON u.id = m.user_id WHERE m.team_id = $1 ORDER BY m.created_at`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.TeamMember{}
	for rows.Next() {
		var m model.TeamMember
		if err := rows.Scan(&m.TeamID, &m.UserID, &m.Email, &m.Name, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// MemberRoles returns team ID → role for a user.
func (r *Repo) MemberRoles(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := r.db.Query(ctx, `SELECT team_id, role FROM team_members WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var role string
		if err := rows.Scan(&id, &role); err != nil {
			return nil, err
		}
		out[id] = role
	}
	return out, rows.Err()
}

// Budgets ----------------------------------------------------------------------------

func (r *Repo) TeamBudget(ctx context.Context, teamID uuid.UUID) (*model.TeamBudget, error) {
	b := &model.TeamBudget{TeamID: teamID, WarnPct: 80}
	err := r.db.QueryRow(ctx, `SELECT monthly_limit_nanos, daily_limit_nanos, warn_threshold_pct FROM team_budgets WHERE team_id = $1`, teamID).
		Scan(&b.MonthlyLimit, &b.DailyLimit, &b.WarnPct)
	if err = mapErr(err); err != nil && err != ErrNotFound {
		return nil, err
	}
	return b, nil
}

func (r *Repo) UpsertTeamBudget(ctx context.Context, b *model.TeamBudget) error {
	_, err := r.db.Exec(ctx, `INSERT INTO team_budgets (team_id, monthly_limit_nanos, daily_limit_nanos, warn_threshold_pct) VALUES ($1,$2,$3,$4)
		ON CONFLICT (team_id) DO UPDATE SET monthly_limit_nanos = EXCLUDED.monthly_limit_nanos, daily_limit_nanos = EXCLUDED.daily_limit_nanos,
		warn_threshold_pct = EXCLUDED.warn_threshold_pct, updated_at = now()`, b.TeamID, b.MonthlyLimit, b.DailyLimit, b.WarnPct)
	return mapErr(err)
}
