// Package handler contains the Gin HTTP handlers for the data plane (/v1) and
// the control plane (/api/v1).
package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"service/internal/budget"
	"service/internal/config"
	"service/internal/gateway"
	"service/internal/ratelimit"
	"service/internal/repository"
	"service/internal/service"
)

type Handler struct {
	Cfg       *config.Config
	Log       *slog.Logger
	Repo      *repository.Repo
	Engine    *gateway.Engine
	Auth      *service.AuthService
	Keys      *service.KeyAuth
	Teams     *service.TeamService
	Providers *service.ProviderService
	Budgets   *budget.Service
	Limiter   *ratelimit.Limiter
}

const (
	sessionCookie = "gw_session"
	ctxPrincipal  = "principal"
	ctxKey        = "api_key_principal"
)

// errorBody is the common error envelope.
func errorBody(typ, code, msg string) gin.H {
	return gin.H{"error": gin.H{"type": typ, "code": code, "message": msg}}
}

// fail writes err as JSON, hiding internal errors.
func (h *Handler) fail(c *gin.Context, err error) {
	var se *service.Error
	if errors.As(err, &se) {
		c.AbortWithStatusJSON(se.Status, errorBody("invalid_request_error", se.Code, se.Message))
		return
	}
	switch {
	case errors.Is(err, repository.ErrNotFound):
		c.AbortWithStatusJSON(http.StatusNotFound, errorBody("invalid_request_error", "not_found", "resource not found"))
		return
	case errors.Is(err, repository.ErrConflict):
		c.AbortWithStatusJSON(http.StatusConflict, errorBody("invalid_request_error", "conflict", "resource already exists"))
		return
	}
	_ = c.Error(err)
	h.Log.Error("request failed", "error", err, "route", c.FullPath())
	c.AbortWithStatusJSON(http.StatusInternalServerError, errorBody("server_error", "internal_error", "internal server error"))
}

// bind strictly decodes a JSON body into v.
func bind(c *gin.Context, v any) error {
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return &service.Error{Status: http.StatusRequestEntityTooLarge, Code: "request_too_large", Message: "request body too large"}
		}
		if errors.Is(err, io.EOF) {
			return service.Invalid("request body is required")
		}
		return service.Invalid("invalid JSON body: %s", err.Error())
	}
	if dec.More() {
		return service.Invalid("request body must contain a single JSON object")
	}
	return nil
}

func paramID(c *gin.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		return uuid.Nil, service.ErrNotFound
	}
	return id, nil
}

func queryUUID(c *gin.Context, name string) (*uuid.UUID, error) {
	v := c.Query(name)
	if v == "" {
		return nil, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil, service.Invalid("%s must be a UUID", name)
	}
	return &id, nil
}

// timeRange parses from/to (RFC3339 or YYYY-MM-DD); default is the last 7 days.
func timeRange(c *gin.Context) (time.Time, time.Time, error) {
	parse := func(name string, def time.Time) (time.Time, error) {
		v := c.Query(name)
		if v == "" {
			return def, nil
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, nil
		}
		if t, err := time.Parse("2006-01-02", v); err == nil {
			return t, nil
		}
		return time.Time{}, service.Invalid("%s must be RFC3339 or YYYY-MM-DD", name)
	}
	now := time.Now().UTC()
	to, err := parse("to", now)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	from, err := parse("from", to.Add(-7*24*time.Hour))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !from.Before(to) || to.Sub(from) > 400*24*time.Hour {
		return time.Time{}, time.Time{}, service.Invalid("from must be before to and the range at most 400 days")
	}
	return from, to, nil
}

func queryInt(c *gin.Context, name string, def int) int {
	if n, err := strconv.Atoi(c.Query(name)); err == nil {
		return n
	}
	return def
}

// Session middleware ----------------------------------------------------------------

// RequireSession authenticates the console session cookie and enforces the CSRF rule.
func (h *Handler) RequireSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if m := c.Request.Method; m != http.MethodGet && m != http.MethodHead {
			if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
				c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, errorBody("invalid_request_error", "unsupported_media_type", "mutating requests must use Content-Type: application/json"))
				return
			}
		}
		token, _ := c.Cookie(sessionCookie)
		p, err := h.Auth.Authenticate(c.Request.Context(), token, c.ClientIP())
		if err != nil {
			h.fail(c, err)
			return
		}
		c.Set(ctxPrincipal, p)
		c.Next()
	}
}

func principal(c *gin.Context) *service.Principal {
	return c.MustGet(ctxPrincipal).(*service.Principal)
}

// RequireAPIKey authenticates data-plane requests.
func (h *Handler) RequireAPIKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok {
			ae := gateway.ErrUnauthorized("missing API key: use 'Authorization: Bearer <key>'")
			c.AbortWithStatusJSON(ae.Status, ae.Body())
			return
		}
		kp, err := h.Keys.Authenticate(c.Request.Context(), strings.TrimSpace(raw))
		if errors.Is(err, service.ErrInvalidKey) {
			ae := gateway.ErrUnauthorized("invalid, revoked or expired API key")
			c.AbortWithStatusJSON(ae.Status, ae.Body())
			return
		}
		if err != nil {
			h.Log.Error("api key lookup failed", "error", err)
			ae := gateway.ErrServiceUnavailable("authentication backend unavailable")
			c.AbortWithStatusJSON(ae.Status, ae.Body())
			return
		}
		c.Set(ctxKey, kp)
		c.Set("team_id", kp.Team.ID.String())
		c.Next()
	}
}

// Health -------------------------------------------------------------------------------

func (h *Handler) Healthz(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) }

func (h *Handler) Readyz(c *gin.Context) {
	ctx := c.Request.Context()
	checks := gin.H{}
	ok := true
	if err := h.Repo.Ping(ctx); err != nil {
		checks["postgres"], ok = "down", false
	} else {
		checks["postgres"] = "up"
	}
	if err := h.Limiter.Ping(ctx); err != nil {
		checks["redis"] = "down"
		if !h.Cfg.RateLimitFailOpen {
			ok = false
		}
	} else {
		checks["redis"] = "up"
	}
	status := http.StatusOK
	if !ok {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{"status": map[bool]string{true: "ready", false: "unavailable"}[ok], "checks": checks})
}

func retryAfterSeconds(d time.Duration) string {
	s := int(d / time.Second)
	if d%time.Second != 0 {
		s++
	}
	if s < 1 {
		s = 1
	}
	return fmt.Sprint(s)
}

var (
	errInvalidTeamParam = service.Invalid("team_id query parameter must be a team UUID")
	errArchived         = service.Invalid("team is archived")
)
