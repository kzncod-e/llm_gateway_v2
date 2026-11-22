// Package usage persists the outcome of a gateway request: the logical request,
// every provider attempt, the per-attempt usage ledger and the budget settlement,
// all in one transaction.
package usage

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"service/internal/budget"
	"service/internal/provider"
)

const (
	StatusSuccess         = "success"
	StatusError           = "error"
	StatusInterrupted     = "interrupted"
	StatusClientCancelled = "client_cancelled"
	StatusRejected        = "rejected"
	StatusSkipped         = "skipped"

	CostExact     = "exact"
	CostEstimated = "estimated"
	CostUnknown   = "unknown"
	CostNone      = "none"
)

// UsageRecord is the financial ledger entry for one attempt.
type UsageRecord struct {
	Usage     provider.Usage
	PricingID *uuid.UUID
	CostNanos *int64 // nil when unknown
	// ChargeNanos is what the attempt counts against the budget: the exact cost,
	// or the reservation estimate when usage is unknown.
	ChargeNanos int64
}

type AttemptRecord struct {
	ID           uuid.UUID
	Number       int
	ProviderID   uuid.UUID
	ModelID      uuid.UUID
	ProviderSlug string
	ModelName    string
	Status       string
	HTTPStatus   *int
	ErrorKind    string
	ErrorMessage string
	Retryable    bool
	StartedAt    time.Time
	Duration     time.Duration
	TTFB         *time.Duration
	Usage        *UsageRecord // nil when the request never reached the provider
}

type RequestRecord struct {
	ID              uuid.UUID
	TeamID          uuid.UUID
	APIKeyID        *uuid.UUID
	UserID          *uuid.UUID
	Source          string
	RequestedModel  string
	Stream          bool
	Status          string
	HTTPStatus      int
	ErrorCode       string
	ErrorMessage    string
	FinalProviderID *uuid.UUID
	FinalModelID    *uuid.UUID
	FallbackCount   int
	Attempts        []AttemptRecord
	Reservation     *budget.Reservation
	CreatedAt       time.Time
	CompletedAt     time.Time
	TTFB            *time.Duration
}

// Totals aggregates attempts into request-level usage and cost.
type Totals struct {
	InputTokens, OutputTokens, TotalTokens *int64
	UsageStatus                            string
	CostNanos                              *int64
	CostStatus                             string
	ChargeNanos                            int64
}

func (r *RequestRecord) Totals() Totals {
	t := Totals{UsageStatus: string(provider.UsageNone), CostStatus: CostNone}
	var in, out, total, cost int64
	anyReported, anyUnknown, anyUnpriced, anyUsage := false, false, false, false
	for _, a := range r.Attempts {
		u := a.Usage
		if u == nil {
			continue
		}
		t.ChargeNanos += u.ChargeNanos
		switch u.Usage.Status {
		case provider.UsageReported:
			anyReported, anyUsage = true, true
			in += u.Usage.TotalInput()
			out += u.Usage.OutputTokens
			total += u.Usage.Total()
			if u.CostNanos != nil {
				cost += *u.CostNanos
			} else {
				anyUnpriced = true
			}
		case provider.UsageUnknown:
			anyUnknown, anyUsage = true, true
			if u.PricingID == nil {
				anyUnpriced = true
			}
		}
	}
	if anyReported {
		t.InputTokens, t.OutputTokens, t.TotalTokens = &in, &out, &total
	}
	switch {
	case anyUnknown:
		t.UsageStatus = string(provider.UsageUnknown)
	case anyReported:
		t.UsageStatus = string(provider.UsageReported)
	}
	switch {
	case !anyUsage:
		t.CostStatus = CostNone
	case anyUnpriced:
		t.CostStatus = CostUnknown
	case anyUnknown:
		t.CostStatus = CostEstimated
	default:
		t.CostStatus = CostExact
	}
	if anyUsage && !anyUnpriced {
		c := cost
		if anyUnknown {
			c = t.ChargeNanos // best estimate includes unknown attempts at their reservation
		}
		t.CostNanos = &c
	}
	return t
}

type Recorder struct {
	pool *pgxpool.Pool
}

func NewRecorder(pool *pgxpool.Pool) *Recorder { return &Recorder{pool: pool} }

func ms(d time.Duration) int { return int(d / time.Millisecond) }

func msPtr(d *time.Duration) *int {
	if d == nil {
		return nil
	}
	v := ms(*d)
	return &v
}

// Record writes the request and settles its reservation atomically.
func (rc *Recorder) Record(ctx context.Context, r *RequestRecord) error {
	tx, err := rc.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	t := r.Totals()
	_, err = tx.Exec(ctx, `INSERT INTO gateway_requests (id, team_id, api_key_id, user_id, source, requested_model, stream, status, http_status,
		error_code, error_message, final_provider_id, final_model_id, attempt_count, fallback_count, input_tokens, output_tokens, total_tokens,
		usage_status, cost_nanos, cost_status, budget_charge_nanos, latency_ms, ttfb_ms, created_at, completed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)`,
		r.ID, r.TeamID, r.APIKeyID, r.UserID, r.Source, r.RequestedModel, r.Stream, r.Status, r.HTTPStatus,
		truncate(r.ErrorCode, 100), truncate(r.ErrorMessage, 1000), r.FinalProviderID, r.FinalModelID, countAttempts(r.Attempts), r.FallbackCount,
		t.InputTokens, t.OutputTokens, t.TotalTokens, t.UsageStatus, t.CostNanos, t.CostStatus, t.ChargeNanos,
		ms(r.CompletedAt.Sub(r.CreatedAt)), msPtr(r.TTFB), r.CreatedAt, r.CompletedAt)
	if err != nil {
		return fmt.Errorf("insert request: %w", err)
	}

	for _, a := range r.Attempts {
		if _, err := tx.Exec(ctx, `INSERT INTO provider_attempts (id, request_id, attempt_number, provider_id, model_id, provider_slug, model_name,
			status, http_status, error_kind, error_message, retryable, started_at, duration_ms, ttfb_ms)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			a.ID, r.ID, a.Number, a.ProviderID, a.ModelID, a.ProviderSlug, a.ModelName, a.Status, a.HTTPStatus,
			a.ErrorKind, truncate(a.ErrorMessage, 1000), a.Retryable, a.StartedAt, ms(a.Duration), msPtr(a.TTFB)); err != nil {
			return fmt.Errorf("insert attempt: %w", err)
		}
		if a.Usage == nil {
			continue
		}
		if err := insertUsage(ctx, tx, r, &a); err != nil {
			return err
		}
	}

	if err := budget.Settle(ctx, tx, r.TeamID, r.Reservation, t.ChargeNanos, r.CreatedAt); err != nil {
		return fmt.Errorf("settle budget: %w", err)
	}
	return tx.Commit(ctx)
}

func insertUsage(ctx context.Context, tx pgx.Tx, r *RequestRecord, a *AttemptRecord) error {
	u := a.Usage
	var in, cached, cw, out, reasoning, total *int64
	if u.Usage.Status != provider.UsageNone {
		// For unknown usage these are partial counts (whatever the provider sent).
		vals := []int64{u.Usage.InputTokens, u.Usage.CachedInputTokens, u.Usage.CacheWriteTokens, u.Usage.OutputTokens, u.Usage.ReasoningTokens, u.Usage.Total()}
		ptrs := []**int64{&in, &cached, &cw, &out, &reasoning, &total}
		for i := range vals {
			v := vals[i]
			*ptrs[i] = &v
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO usage_records (id, attempt_id, request_id, team_id, api_key_id, provider_id, model_id, pricing_id,
		usage_status, input_tokens, cached_input_tokens, cache_write_tokens, output_tokens, reasoning_tokens, total_tokens, cost_nanos, currency, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'USD',$17)`,
		uuid.Must(uuid.NewV7()), a.ID, r.ID, r.TeamID, r.APIKeyID, a.ProviderID, a.ModelID, u.PricingID,
		string(u.Usage.Status), in, cached, cw, out, reasoning, total, u.CostNanos, a.StartedAt)
	if err != nil {
		return fmt.Errorf("insert usage: %w", err)
	}
	return nil
}

func countAttempts(as []AttemptRecord) int {
	n := 0
	for _, a := range as {
		if a.Status != StatusSkipped {
			n++
		}
	}
	return n
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
