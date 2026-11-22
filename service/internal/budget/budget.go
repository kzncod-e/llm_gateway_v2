// Package budget enforces per-team spend limits with a reservation ledger in
// Postgres (see ADR-005). A conditional UPDATE on the team's period row makes
// concurrent reservations serialize, so they can never jointly exceed the limit.
package budget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"service/internal/model"
)

var ErrExceeded = errors.New("budget exceeded")

// ExceededError says which period limit was hit.
type ExceededError struct {
	Period string // month | day
	Limit  int64
}

func (e *ExceededError) Error() string { return fmt.Sprintf("%s budget exceeded", e.Period) }
func (e *ExceededError) Unwrap() error { return ErrExceeded }

type Reservation struct {
	ID         uuid.UUID
	TeamID     uuid.UUID
	Amount     int64
	MonthStart time.Time
	DayStart   time.Time
}

type Service struct {
	pool *pgxpool.Pool
	ttl  time.Duration
	now  func() time.Time
}

func New(pool *pgxpool.Pool, ttl time.Duration) *Service {
	return &Service{pool: pool, ttl: ttl, now: time.Now}
}

// PeriodStarts returns the UTC calendar month and day containing t.
func PeriodStarts(t time.Time) (month, day time.Time) {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC), time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Reserve reserves amount nanos for a request. hasBudget is false (and no
// reservation is made) when the team has no limits configured.
func (s *Service) Reserve(ctx context.Context, teamID, requestID uuid.UUID, amount int64) (res *Reservation, hasBudget bool, err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var monthly, daily *int64
	err = tx.QueryRow(ctx, `SELECT monthly_limit_nanos, daily_limit_nanos FROM team_budgets WHERE team_id = $1`, teamID).Scan(&monthly, &daily)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && monthly == nil && daily == nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	month, day := PeriodStarts(s.now())
	periods := []struct {
		typ   string
		start time.Time
		limit *int64
	}{{"month", month, monthly}, {"day", day, daily}}

	for _, p := range periods {
		if _, err := tx.Exec(ctx, `INSERT INTO budget_periods (team_id, period_type, period_start) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, teamID, p.typ, p.start); err != nil {
			return nil, true, err
		}
		var tag int64
		if p.limit == nil {
			err = tx.QueryRow(ctx, `UPDATE budget_periods SET reserved_nanos = reserved_nanos + $4, updated_at = now()
				WHERE team_id = $1 AND period_type = $2 AND period_start = $3 RETURNING 1`, teamID, p.typ, p.start, amount).Scan(&tag)
		} else {
			err = tx.QueryRow(ctx, `UPDATE budget_periods SET reserved_nanos = reserved_nanos + $4, updated_at = now()
				WHERE team_id = $1 AND period_type = $2 AND period_start = $3
				  AND spent_nanos + reserved_nanos + $4 <= $5 RETURNING 1`, teamID, p.typ, p.start, amount, *p.limit).Scan(&tag)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, true, &ExceededError{Period: p.typ, Limit: *p.limit}
		}
		if err != nil {
			return nil, true, err
		}
	}

	res = &Reservation{ID: model.NewID(), TeamID: teamID, Amount: amount, MonthStart: month, DayStart: day}
	if _, err := tx.Exec(ctx, `INSERT INTO budget_reservations (id, team_id, request_id, amount_nanos, month_start, day_start, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'active', $7)`, res.ID, teamID, requestID, amount, month, day, s.now().Add(s.ttl)); err != nil {
		return nil, true, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, true, err
	}
	return res, true, nil
}

// Settle finalizes a reservation (if any) and adds charge to the team's spend for
// the periods containing at. It must run inside the caller's transaction so that
// spend and the usage ledger are committed atomically.
func Settle(ctx context.Context, tx pgx.Tx, teamID uuid.UUID, res *Reservation, charge int64, at time.Time) error {
	if res != nil {
		var amount int64
		var monthStart, dayStart time.Time
		err := tx.QueryRow(ctx, `UPDATE budget_reservations SET status = 'settled', finalized_at = now()
			WHERE id = $1 AND status = 'active' RETURNING amount_nanos, month_start, day_start`, res.ID).Scan(&amount, &monthStart, &dayStart)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Already released by the sweeper; its reserved amount was returned then.
		case err != nil:
			return err
		default:
			if err := releaseReserved(ctx, tx, teamID, amount, monthStart, dayStart); err != nil {
				return err
			}
		}
	}
	if charge <= 0 {
		return nil
	}
	month, day := PeriodStarts(at)
	_, err := tx.Exec(ctx, `INSERT INTO budget_periods (team_id, period_type, period_start, spent_nanos)
		VALUES ($1, 'month', $2, $4), ($1, 'day', $3, $4)
		ON CONFLICT (team_id, period_type, period_start)
		DO UPDATE SET spent_nanos = budget_periods.spent_nanos + EXCLUDED.spent_nanos, updated_at = now()`,
		teamID, month, day, charge)
	return err
}

func releaseReserved(ctx context.Context, tx pgx.Tx, teamID uuid.UUID, amount int64, month, day time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE budget_periods SET reserved_nanos = GREATEST(reserved_nanos - $2, 0), updated_at = now()
		WHERE team_id = $1 AND ((period_type = 'month' AND period_start = $3) OR (period_type = 'day' AND period_start = $4))`,
		teamID, amount, month, day)
	return err
}

// Release returns an unused reservation without charging anything.
func (s *Service) Release(ctx context.Context, res *Reservation) error {
	if res == nil {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := Settle(ctx, tx, res.TeamID, res, 0, s.now()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE budget_reservations SET status = 'released' WHERE id = $1`, res.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SweepExpired releases reservations whose request never settled (e.g. crash).
func (s *Service) SweepExpired(ctx context.Context) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `UPDATE budget_reservations SET status = 'released', finalized_at = now()
		WHERE id IN (SELECT id FROM budget_reservations WHERE status = 'active' AND expires_at < now()
		             ORDER BY expires_at LIMIT 500 FOR UPDATE SKIP LOCKED)
		RETURNING team_id, amount_nanos, month_start, day_start`)
	if err != nil {
		return 0, err
	}
	type item struct {
		team       uuid.UUID
		amount     int64
		month, day time.Time
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.team, &it.amount, &it.month, &it.day); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, it := range items {
		if err := releaseReserved(ctx, tx, it.team, it.amount, it.month, it.day); err != nil {
			return 0, err
		}
	}
	return len(items), tx.Commit(ctx)
}

// Status summarizes a team's budget for the console.
type Status struct {
	TeamID         uuid.UUID `json:"team_id"`
	MonthlyLimit   *int64    `json:"monthly_limit_nanos"`
	DailyLimit     *int64    `json:"daily_limit_nanos"`
	WarnPct        int       `json:"warn_threshold_pct"`
	MonthSpent     int64     `json:"month_spent_nanos"`
	MonthReserved  int64     `json:"month_reserved_nanos"`
	DaySpent       int64     `json:"day_spent_nanos"`
	DayReserved    int64     `json:"day_reserved_nanos"`
	MonthRemaining *int64    `json:"month_remaining_nanos"`
	DayRemaining   *int64    `json:"day_remaining_nanos"`
	Warning        bool      `json:"warning"`
	Exhausted      bool      `json:"exhausted"`
	PeriodMonth    string    `json:"period_month"`
}

func (s *Service) Status(ctx context.Context, teamID uuid.UUID) (*Status, error) {
	st := &Status{TeamID: teamID, WarnPct: 80}
	err := s.pool.QueryRow(ctx, `SELECT monthly_limit_nanos, daily_limit_nanos, warn_threshold_pct FROM team_budgets WHERE team_id = $1`, teamID).
		Scan(&st.MonthlyLimit, &st.DailyLimit, &st.WarnPct)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	month, day := PeriodStarts(s.now())
	st.PeriodMonth = month.Format("2006-01")
	rows, err := s.pool.Query(ctx, `SELECT period_type, spent_nanos, reserved_nanos FROM budget_periods
		WHERE team_id = $1 AND ((period_type = 'month' AND period_start = $2) OR (period_type = 'day' AND period_start = $3))`, teamID, month, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var typ string
		var spent, reserved int64
		if err := rows.Scan(&typ, &spent, &reserved); err != nil {
			return nil, err
		}
		if typ == "month" {
			st.MonthSpent, st.MonthReserved = spent, reserved
		} else {
			st.DaySpent, st.DayReserved = spent, reserved
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	check := func(limit *int64, spent int64) *int64 {
		if limit == nil {
			return nil
		}
		rem := *limit - spent
		if rem < 0 {
			rem = 0
		}
		if *limit > 0 && spent*100 >= *limit*int64(st.WarnPct) {
			st.Warning = true
		}
		if spent >= *limit {
			st.Exhausted = true
		}
		return &rem
	}
	st.MonthRemaining = check(st.MonthlyLimit, st.MonthSpent)
	st.DayRemaining = check(st.DailyLimit, st.DaySpent)
	return st, nil
}
