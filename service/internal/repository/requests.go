package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type RequestFilter struct {
	TeamIDs    []uuid.UUID // allowed teams; nil = all
	TeamID     *uuid.UUID
	ProviderID *uuid.UUID
	Status     string
	Model      string
	Source     string
	RequestID  *uuid.UUID
	From, To   *time.Time
	Before     *Cursor
	Limit      int
}

type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type RequestRow struct {
	ID              uuid.UUID  `json:"id"`
	TeamID          uuid.UUID  `json:"team_id"`
	TeamName        string     `json:"team_name"`
	APIKeyID        *uuid.UUID `json:"api_key_id"`
	APIKeyPrefix    *string    `json:"api_key_prefix"`
	UserID          *uuid.UUID `json:"user_id"`
	Source          string     `json:"source"`
	RequestedModel  string     `json:"requested_model"`
	Stream          bool       `json:"stream"`
	Status          string     `json:"status"`
	HTTPStatus      int        `json:"http_status"`
	ErrorCode       string     `json:"error_code"`
	ErrorMessage    string     `json:"error_message"`
	ProviderSlug    *string    `json:"provider_slug"`
	ModelName       *string    `json:"model_name"`
	AttemptCount    int        `json:"attempt_count"`
	FallbackCount   int        `json:"fallback_count"`
	InputTokens     *int64     `json:"input_tokens"`
	OutputTokens    *int64     `json:"output_tokens"`
	TotalTokens     *int64     `json:"total_tokens"`
	UsageStatus     string     `json:"usage_status"`
	CostNanos       *int64     `json:"cost_nanos"`
	CostStatus      string     `json:"cost_status"`
	BudgetCharge    int64      `json:"budget_charge_nanos"`
	LatencyMs       int        `json:"latency_ms"`
	TTFBMs          *int       `json:"ttfb_ms"`
	CreatedAt       time.Time  `json:"created_at"`
	CompletedAt     time.Time  `json:"completed_at"`
}

const requestSelect = `SELECT r.id, r.team_id, t.name, r.api_key_id, k.prefix, r.user_id, r.source, r.requested_model, r.stream, r.status, r.http_status,
	r.error_code, r.error_message, p.slug, m.name, r.attempt_count, r.fallback_count, r.input_tokens, r.output_tokens, r.total_tokens,
	r.usage_status, r.cost_nanos, r.cost_status, r.budget_charge_nanos, r.latency_ms, r.ttfb_ms, r.created_at, r.completed_at
	FROM gateway_requests r
	JOIN teams t ON t.id = r.team_id
	LEFT JOIN api_keys k ON k.id = r.api_key_id
	LEFT JOIN providers p ON p.id = r.final_provider_id
	LEFT JOIN models m ON m.id = r.final_model_id`

type scanner interface{ Scan(dest ...any) error }

func scanRequest(s scanner) (*RequestRow, error) {
	var x RequestRow
	err := s.Scan(&x.ID, &x.TeamID, &x.TeamName, &x.APIKeyID, &x.APIKeyPrefix, &x.UserID, &x.Source, &x.RequestedModel, &x.Stream, &x.Status, &x.HTTPStatus,
		&x.ErrorCode, &x.ErrorMessage, &x.ProviderSlug, &x.ModelName, &x.AttemptCount, &x.FallbackCount, &x.InputTokens, &x.OutputTokens, &x.TotalTokens,
		&x.UsageStatus, &x.CostNanos, &x.CostStatus, &x.BudgetCharge, &x.LatencyMs, &x.TTFBMs, &x.CreatedAt, &x.CompletedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &x, nil
}

// where builds a parameterized WHERE clause.
type where struct {
	parts []string
	args  []any
}

func (w *where) add(cond string, arg any) {
	w.args = append(w.args, arg)
	w.parts = append(w.parts, strings.ReplaceAll(cond, "?", fmt.Sprintf("$%d", len(w.args))))
}

func (w *where) sql() string {
	if len(w.parts) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.parts, " AND ")
}

func (r *Repo) ListRequests(ctx context.Context, f RequestFilter) ([]*RequestRow, *Cursor, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	w := &where{}
	if f.TeamIDs != nil {
		w.add("r.team_id = ANY(?)", f.TeamIDs)
	}
	if f.TeamID != nil {
		w.add("r.team_id = ?", *f.TeamID)
	}
	if f.ProviderID != nil {
		w.add("r.final_provider_id = ?", *f.ProviderID)
	}
	if f.Status != "" {
		w.add("r.status = ?", f.Status)
	}
	if f.Source != "" {
		w.add("r.source = ?", f.Source)
	}
	if f.Model != "" {
		w.add("r.requested_model ILIKE ?", "%"+escapeLike(f.Model)+"%")
	}
	if f.RequestID != nil {
		w.add("r.id = ?", *f.RequestID)
	}
	if f.From != nil {
		w.add("r.created_at >= ?", *f.From)
	}
	if f.To != nil {
		w.add("r.created_at < ?", *f.To)
	}
	if f.Before != nil {
		w.args = append(w.args, f.Before.CreatedAt, f.Before.ID)
		w.parts = append(w.parts, fmt.Sprintf("(r.created_at, r.id) < ($%d, $%d)", len(w.args)-1, len(w.args)))
	}
	w.args = append(w.args, f.Limit+1)
	q := requestSelect + w.sql() + fmt.Sprintf(" ORDER BY r.created_at DESC, r.id DESC LIMIT $%d", len(w.args))
	rows, err := r.db.Query(ctx, q, w.args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []*RequestRow{}
	for rows.Next() {
		x, err := scanRequest(rows)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *Cursor
	if len(out) > f.Limit {
		out = out[:f.Limit]
		last := out[len(out)-1]
		next = &Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return out, next, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

type AttemptRow struct {
	ID                uuid.UUID `json:"id"`
	AttemptNumber     int       `json:"attempt_number"`
	ProviderSlug      string    `json:"provider_slug"`
	ModelName         string    `json:"model_name"`
	Status            string    `json:"status"`
	HTTPStatus        *int      `json:"http_status"`
	ErrorKind         string    `json:"error_kind"`
	ErrorMessage      string    `json:"error_message"`
	Retryable         bool      `json:"retryable"`
	StartedAt         time.Time `json:"started_at"`
	DurationMs        int       `json:"duration_ms"`
	TTFBMs            *int      `json:"ttfb_ms"`
	UsageStatus       *string   `json:"usage_status"`
	InputTokens       *int64    `json:"input_tokens"`
	CachedInputTokens *int64    `json:"cached_input_tokens"`
	CacheWriteTokens  *int64    `json:"cache_write_tokens"`
	OutputTokens      *int64    `json:"output_tokens"`
	ReasoningTokens   *int64    `json:"reasoning_tokens"`
	TotalTokens       *int64    `json:"total_tokens"`
	CostNanos         *int64    `json:"cost_nanos"`
	PricingID         *uuid.UUID `json:"pricing_id"`
}

type RequestDetail struct {
	*RequestRow
	Attempts []*AttemptRow `json:"attempts"`
}

func (r *Repo) RequestDetail(ctx context.Context, id uuid.UUID, teamIDs []uuid.UUID) (*RequestDetail, error) {
	w := &where{}
	w.add("r.id = ?", id)
	if teamIDs != nil {
		w.add("r.team_id = ANY(?)", teamIDs)
	}
	row, err := scanRequest(r.db.QueryRow(ctx, requestSelect+w.sql(), w.args...))
	if err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx, `SELECT a.id, a.attempt_number, a.provider_slug, a.model_name, a.status, a.http_status, a.error_kind, a.error_message,
		a.retryable, a.started_at, a.duration_ms, a.ttfb_ms, u.usage_status, u.input_tokens, u.cached_input_tokens, u.cache_write_tokens,
		u.output_tokens, u.reasoning_tokens, u.total_tokens, u.cost_nanos, u.pricing_id
		FROM provider_attempts a LEFT JOIN usage_records u ON u.attempt_id = a.id
		WHERE a.request_id = $1 ORDER BY a.attempt_number`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d := &RequestDetail{RequestRow: row, Attempts: []*AttemptRow{}}
	for rows.Next() {
		var a AttemptRow
		if err := rows.Scan(&a.ID, &a.AttemptNumber, &a.ProviderSlug, &a.ModelName, &a.Status, &a.HTTPStatus, &a.ErrorKind, &a.ErrorMessage,
			&a.Retryable, &a.StartedAt, &a.DurationMs, &a.TTFBMs, &a.UsageStatus, &a.InputTokens, &a.CachedInputTokens, &a.CacheWriteTokens,
			&a.OutputTokens, &a.ReasoningTokens, &a.TotalTokens, &a.CostNanos, &a.PricingID); err != nil {
			return nil, err
		}
		d.Attempts = append(d.Attempts, &a)
	}
	return d, rows.Err()
}
