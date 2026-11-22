package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type AnalyticsFilter struct {
	From, To time.Time
	TeamIDs  []uuid.UUID // allowed; nil = all
	TeamID   *uuid.UUID
}

func (f AnalyticsFilter) where(alias string) *where {
	w := &where{}
	w.add(alias+".created_at >= ?", f.From)
	w.add(alias+".created_at < ?", f.To)
	if f.TeamIDs != nil {
		w.add(alias+".team_id = ANY(?)", f.TeamIDs)
	}
	if f.TeamID != nil {
		w.add(alias+".team_id = ?", *f.TeamID)
	}
	return w
}

type Summary struct {
	Requests           int64   `json:"requests"`
	Succeeded          int64   `json:"succeeded"`
	Failed             int64   `json:"failed"`
	Rejected           int64   `json:"rejected"`
	SuccessRate        float64 `json:"success_rate"`
	CostNanos          int64   `json:"cost_nanos"`
	EstimatedCostNanos int64   `json:"estimated_cost_nanos"`
	UnpricedRequests   int64   `json:"unpriced_requests"`
	InputTokens        int64   `json:"input_tokens"`
	OutputTokens       int64   `json:"output_tokens"`
	TotalTokens        int64   `json:"total_tokens"`
	AvgLatencyMs       float64 `json:"avg_latency_ms"`
	P95LatencyMs       float64 `json:"p95_latency_ms"`
	Fallbacks          int64   `json:"fallbacks"`
	StreamRequests     int64   `json:"stream_requests"`
}

func (r *Repo) Summary(ctx context.Context, f AnalyticsFilter) (*Summary, error) {
	w := f.where("r")
	var s Summary
	err := r.db.QueryRow(ctx, `SELECT count(*),
		count(*) FILTER (WHERE status = 'success'),
		count(*) FILTER (WHERE status IN ('error', 'interrupted')),
		count(*) FILTER (WHERE status = 'rejected'),
		COALESCE(sum(cost_nanos), 0)::bigint,
		COALESCE(sum(cost_nanos) FILTER (WHERE cost_status = 'estimated'), 0)::bigint,
		count(*) FILTER (WHERE cost_status = 'unknown'),
		COALESCE(sum(input_tokens), 0)::bigint, COALESCE(sum(output_tokens), 0)::bigint, COALESCE(sum(total_tokens), 0)::bigint,
		COALESCE(avg(latency_ms) FILTER (WHERE status <> 'rejected'), 0)::float8,
		COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE status <> 'rejected'), 0)::float8,
		COALESCE(sum(fallback_count), 0)::bigint,
		count(*) FILTER (WHERE stream)
		FROM gateway_requests r`+w.sql(), w.args...).
		Scan(&s.Requests, &s.Succeeded, &s.Failed, &s.Rejected, &s.CostNanos, &s.EstimatedCostNanos, &s.UnpricedRequests,
			&s.InputTokens, &s.OutputTokens, &s.TotalTokens, &s.AvgLatencyMs, &s.P95LatencyMs, &s.Fallbacks, &s.StreamRequests)
	if err != nil {
		return nil, err
	}
	if considered := s.Requests - s.Rejected; considered > 0 {
		s.SuccessRate = float64(s.Succeeded) / float64(considered)
	}
	return &s, nil
}

type TimePoint struct {
	Bucket       time.Time `json:"bucket"`
	Requests     int64     `json:"requests"`
	Failed       int64     `json:"failed"`
	CostNanos    int64     `json:"cost_nanos"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	AvgLatencyMs float64   `json:"avg_latency_ms"`
}

// Timeseries buckets requests by hour or day, filling empty buckets with zeros.
func (r *Repo) Timeseries(ctx context.Context, f AnalyticsFilter, interval string) ([]TimePoint, error) {
	if interval != "hour" && interval != "day" {
		return nil, fmt.Errorf("invalid interval %q", interval)
	}
	w := f.where("r")
	n := len(w.args)
	q := fmt.Sprintf(`WITH buckets AS (
		SELECT generate_series(date_trunc('%[1]s', $1::timestamptz), date_trunc('%[1]s', $2::timestamptz - interval '1 microsecond'), interval '1 %[1]s') AS bucket
	), agg AS (
		SELECT date_trunc('%[1]s', created_at) AS bucket, count(*) AS requests,
			count(*) FILTER (WHERE status IN ('error', 'interrupted')) AS failed,
			COALESCE(sum(cost_nanos), 0)::bigint AS cost, COALESCE(sum(input_tokens), 0)::bigint AS input_tokens,
			COALESCE(sum(output_tokens), 0)::bigint AS output_tokens, COALESCE(avg(latency_ms), 0)::float8 AS latency
		FROM gateway_requests r %[2]s GROUP BY 1
	)
	SELECT b.bucket, COALESCE(a.requests, 0), COALESCE(a.failed, 0), COALESCE(a.cost, 0), COALESCE(a.input_tokens, 0), COALESCE(a.output_tokens, 0), COALESCE(a.latency, 0)
	FROM buckets b LEFT JOIN agg a ON a.bucket = b.bucket ORDER BY b.bucket LIMIT 2000`, interval, w.sql())
	_ = n
	rows, err := r.db.Query(ctx, q, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TimePoint{}
	for rows.Next() {
		var p TimePoint
		if err := rows.Scan(&p.Bucket, &p.Requests, &p.Failed, &p.CostNanos, &p.InputTokens, &p.OutputTokens, &p.AvgLatencyMs); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type BreakdownRow struct {
	Key          string  `json:"key"`
	Label        string  `json:"label"`
	Attempts     int64   `json:"attempts"`
	CostNanos    int64   `json:"cost_nanos"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	UnknownUsage int64   `json:"unknown_usage"`
	Share        float64 `json:"share"`
}

// Breakdown aggregates the usage ledger by provider, model or team.
func (r *Repo) Breakdown(ctx context.Context, f AnalyticsFilter, by string) ([]BreakdownRow, error) {
	var key, label, join string
	switch by {
	case "provider":
		key, label, join = "u.provider_id::text", "COALESCE(p.name, 'deleted')", "LEFT JOIN providers p ON p.id = u.provider_id"
	case "model":
		key, label, join = "u.model_id::text", "COALESCE(p.slug || '/' || m.name, 'deleted')", "LEFT JOIN models m ON m.id = u.model_id LEFT JOIN providers p ON p.id = u.provider_id"
	case "team":
		key, label, join = "u.team_id::text", "t.name", "JOIN teams t ON t.id = u.team_id"
	default:
		return nil, fmt.Errorf("invalid breakdown %q", by)
	}
	w := f.where("u")
	w.parts = append(w.parts, "u.usage_status <> 'none'")
	q := fmt.Sprintf(`SELECT COALESCE(%s, ''), %s, count(*), COALESCE(sum(u.cost_nanos), 0)::bigint,
		COALESCE(sum(u.input_tokens + u.cached_input_tokens + u.cache_write_tokens) FILTER (WHERE u.usage_status = 'reported'), 0)::bigint,
		COALESCE(sum(u.output_tokens) FILTER (WHERE u.usage_status = 'reported'), 0)::bigint,
		count(*) FILTER (WHERE u.usage_status = 'unknown')
		FROM usage_records u %s %s GROUP BY 1, 2 ORDER BY 4 DESC, 3 DESC LIMIT 100`, key, label, join, w.sql())
	rows, err := r.db.Query(ctx, q, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BreakdownRow{}
	var total int64
	for rows.Next() {
		var b BreakdownRow
		if err := rows.Scan(&b.Key, &b.Label, &b.Attempts, &b.CostNanos, &b.InputTokens, &b.OutputTokens, &b.UnknownUsage); err != nil {
			return nil, err
		}
		total += b.CostNanos
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if total > 0 {
			out[i].Share = float64(out[i].CostNanos) / float64(total)
		}
	}
	return out, nil
}

// Reliability -------------------------------------------------------------------------

type ProviderStats struct {
	ProviderID   uuid.UUID  `json:"provider_id"`
	Attempts     int64      `json:"attempts"`
	Errors       int64      `json:"errors"`
	Skipped      int64      `json:"skipped"`
	ErrorRate    float64    `json:"error_rate"`
	P50Ms        float64    `json:"p50_ms"`
	P95Ms        float64    `json:"p95_ms"`
	AvgTTFBMs    float64    `json:"avg_ttfb_ms"`
	LastErrorAt  *time.Time `json:"last_error_at"`
	LastError    *string    `json:"last_error"`
}

func (r *Repo) ProviderStats(ctx context.Context, since time.Time) (map[uuid.UUID]*ProviderStats, error) {
	rows, err := r.db.Query(ctx, `SELECT a.provider_id,
		count(*) FILTER (WHERE status <> 'skipped'),
		count(*) FILTER (WHERE status IN ('error', 'interrupted')),
		count(*) FILTER (WHERE status = 'skipped'),
		COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE status = 'success'), 0)::float8,
		COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE status = 'success'), 0)::float8,
		COALESCE(avg(ttfb_ms), 0)::float8,
		max(started_at) FILTER (WHERE status IN ('error', 'interrupted')),
		(array_agg(error_kind || ': ' || error_message ORDER BY started_at DESC) FILTER (WHERE status IN ('error', 'interrupted')))[1]
		FROM provider_attempts a WHERE a.started_at >= $1 AND a.provider_id IS NOT NULL GROUP BY a.provider_id`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]*ProviderStats{}
	for rows.Next() {
		var s ProviderStats
		if err := rows.Scan(&s.ProviderID, &s.Attempts, &s.Errors, &s.Skipped, &s.P50Ms, &s.P95Ms, &s.AvgTTFBMs, &s.LastErrorAt, &s.LastError); err != nil {
			return nil, err
		}
		if s.Attempts > 0 {
			s.ErrorRate = float64(s.Errors) / float64(s.Attempts)
		}
		out[s.ProviderID] = &s
	}
	return out, rows.Err()
}

type ProviderErrorPoint struct {
	Bucket       time.Time `json:"bucket"`
	ProviderSlug string    `json:"provider_slug"`
	Attempts     int64     `json:"attempts"`
	Errors       int64     `json:"errors"`
	P95Ms        float64   `json:"p95_ms"`
}

func (r *Repo) ProviderTimeseries(ctx context.Context, since time.Time) ([]ProviderErrorPoint, error) {
	rows, err := r.db.Query(ctx, `SELECT date_trunc('hour', started_at), provider_slug,
		count(*) FILTER (WHERE status <> 'skipped'), count(*) FILTER (WHERE status IN ('error', 'interrupted')),
		COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE status = 'success'), 0)::float8
		FROM provider_attempts WHERE started_at >= $1 GROUP BY 1, 2 ORDER BY 1, 2`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProviderErrorPoint{}
	for rows.Next() {
		var p ProviderErrorPoint
		if err := rows.Scan(&p.Bucket, &p.ProviderSlug, &p.Attempts, &p.Errors, &p.P95Ms); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
