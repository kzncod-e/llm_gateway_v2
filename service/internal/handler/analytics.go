package handler

import (
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"service/internal/repository"
	"service/internal/service"
)

// analyticsFilter applies team isolation: non-admins only see their teams.
func (h *Handler) analyticsFilter(c *gin.Context) (repository.AnalyticsFilter, error) {
	p := principal(c)
	from, to, err := timeRange(c)
	if err != nil {
		return repository.AnalyticsFilter{}, err
	}
	team, err := queryUUID(c, "team_id")
	if err != nil {
		return repository.AnalyticsFilter{}, err
	}
	if team != nil {
		if err := p.Require(*team, "viewer"); err != nil {
			return repository.AnalyticsFilter{}, err
		}
	}
	return repository.AnalyticsFilter{From: from, To: to, TeamIDs: p.VisibleTeams(), TeamID: team}, nil
}

func (h *Handler) AnalyticsSummary(c *gin.Context) {
	f, err := h.analyticsFilter(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	s, err := h.Repo.Summary(c.Request.Context(), f)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"from": f.From, "to": f.To, "summary": s})
}

func (h *Handler) AnalyticsTimeseries(c *gin.Context) {
	f, err := h.analyticsFilter(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	interval := c.DefaultQuery("interval", "day")
	if interval != "hour" && interval != "day" {
		h.fail(c, service.Invalid("interval must be hour or day"))
		return
	}
	if interval == "hour" && f.To.Sub(f.From) > 31*24*time.Hour {
		h.fail(c, service.Invalid("hourly interval supports ranges up to 31 days"))
		return
	}
	pts, err := h.Repo.Timeseries(c.Request.Context(), f, interval)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"interval": interval, "data": pts})
}

func (h *Handler) AnalyticsBreakdown(c *gin.Context) {
	f, err := h.analyticsFilter(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	by := c.DefaultQuery("by", "provider")
	if by != "provider" && by != "model" && by != "team" {
		h.fail(c, service.Invalid("by must be provider, model or team"))
		return
	}
	rows, err := h.Repo.Breakdown(c.Request.Context(), f, by)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"by": by, "data": rows})
}

// Requests -------------------------------------------------------------------------------

func (h *Handler) ListRequests(c *gin.Context) {
	p := principal(c)
	f := repository.RequestFilter{TeamIDs: p.VisibleTeams(), Status: c.Query("status"), Model: c.Query("model"), Source: c.Query("source"), Limit: queryInt(c, "limit", 50)}
	var err error
	if f.TeamID, err = queryUUID(c, "team_id"); err != nil {
		h.fail(c, err)
		return
	}
	if f.ProviderID, err = queryUUID(c, "provider_id"); err != nil {
		h.fail(c, err)
		return
	}
	if q := c.Query("q"); q != "" {
		if id, err := uuid.Parse(q); err == nil {
			f.RequestID = &id
		} else {
			f.Model = q
		}
	}
	if c.Query("from") != "" || c.Query("to") != "" {
		from, to, err := timeRange(c)
		if err != nil {
			h.fail(c, err)
			return
		}
		f.From, f.To = &from, &to
	}
	if cur := c.Query("cursor"); cur != "" {
		cr, err := decodeCursor(cur)
		if err != nil {
			h.fail(c, service.Invalid("invalid cursor"))
			return
		}
		f.Before = cr
	}
	rows, next, err := h.Repo.ListRequests(c.Request.Context(), f)
	if err != nil {
		h.fail(c, err)
		return
	}
	resp := gin.H{"data": rows, "next_cursor": nil}
	if next != nil {
		resp["next_cursor"] = encodeCursor(next)
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) GetRequest(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	d, err := h.Repo.RequestDetail(c.Request.Context(), id, principal(c).VisibleTeams())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

func encodeCursor(c *repository.Cursor) string {
	return c.CreatedAt.UTC().Format(time.RFC3339Nano) + "_" + c.ID.String()
}

func decodeCursor(s string) (*repository.Cursor, error) {
	if len(s) < 38 {
		return nil, service.Invalid("invalid cursor")
	}
	ts, idPart := s[:len(s)-37], s[len(s)-36:]
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(idPart)
	if err != nil {
		return nil, err
	}
	return &repository.Cursor{CreatedAt: t, ID: id}, nil
}

// Reliability ------------------------------------------------------------------------------

func circuitJSON(state string, failures int, openedAt, lastFailure time.Time, lastErr string) gin.H {
	out := gin.H{"state": state, "consecutive_failures": failures, "last_error": lastErr, "opened_at": nil, "last_failure_at": nil}
	if !openedAt.IsZero() {
		out["opened_at"] = openedAt
	}
	if !lastFailure.IsZero() {
		out["last_failure_at"] = lastFailure
	}
	return out
}

func (h *Handler) Reliability(c *gin.Context) {
	ctx := c.Request.Context()
	p := principal(c)
	window := 24 * time.Hour
	if c.Query("window") == "1h" {
		window = time.Hour
	}
	since := time.Now().Add(-window)
	stats, err := h.Repo.ProviderStats(ctx, since)
	if err != nil {
		h.fail(c, err)
		return
	}
	series, err := h.Repo.ProviderTimeseries(ctx, since)
	if err != nil {
		h.fail(c, err)
		return
	}
	provs, err := h.Providers.List(ctx)
	if err != nil {
		h.fail(c, err)
		return
	}
	snaps := h.Engine.Breakers().Snapshots()
	type row struct {
		ProviderID string                    `json:"provider_id"`
		Slug       string                    `json:"slug"`
		Name       string                    `json:"name"`
		Type       string                    `json:"type"`
		Enabled    bool                      `json:"enabled"`
		Circuit    gin.H                     `json:"circuit"`
		Stats      *repository.ProviderStats `json:"stats"`
	}
	rows := make([]row, 0, len(provs))
	for _, pr := range provs {
		r := row{ProviderID: pr.ID.String(), Slug: pr.Slug, Name: pr.Name, Type: pr.Type, Enabled: pr.Enabled, Stats: stats[pr.ID]}
		if s, ok := snaps[pr.ID.String()]; ok {
			r.Circuit = circuitJSON(s.State.String(), s.ConsecutiveFailures, s.OpenedAt, s.LastFailureAt, s.LastError)
		} else {
			r.Circuit = circuitJSON("closed", 0, time.Time{}, time.Time{}, "")
		}
		if r.Stats == nil {
			r.Stats = &repository.ProviderStats{ProviderID: pr.ID}
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Slug < rows[j].Slug })

	failed, _, err := h.Repo.ListRequests(ctx, repository.RequestFilter{TeamIDs: p.VisibleTeams(), Status: "error", From: &since, Limit: 20})
	if err != nil {
		h.fail(c, err)
		return
	}
	summary, err := h.Repo.Summary(ctx, repository.AnalyticsFilter{From: since, To: time.Now().Add(time.Minute), TeamIDs: p.VisibleTeams()})
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"window": window.String(), "providers": rows, "series": series, "recent_failures": failed, "summary": summary})
}

func (h *Handler) ResetBreaker(c *gin.Context) {
	if err := principal(c).RequireAdmin(); err != nil {
		h.fail(c, err)
		return
	}
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	h.Engine.Breakers().Get(id.String()).Reset()
	c.Status(http.StatusNoContent)
}
