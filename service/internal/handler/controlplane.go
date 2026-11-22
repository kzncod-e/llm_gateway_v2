package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"service/internal/model"
	"service/internal/service"
)

// Auth -----------------------------------------------------------------------------------

type loginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) setSessionCookie(c *gin.Context, token string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: h.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) Login(c *gin.Context) {
	var in loginInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	token, u, err := h.Auth.Login(c.Request.Context(), in.Email, in.Password, c.GetHeader("User-Agent"), c.ClientIP())
	if err != nil {
		h.fail(c, err)
		return
	}
	h.setSessionCookie(c, token, int(h.Cfg.SessionTTL/time.Second))
	c.JSON(http.StatusOK, gin.H{"user": u})
}

func (h *Handler) Logout(c *gin.Context) {
	if token, err := c.Cookie(sessionCookie); err == nil {
		if err := h.Auth.Logout(c.Request.Context(), token); err != nil {
			h.fail(c, err)
			return
		}
	}
	h.setSessionCookie(c, "", -1)
	c.Status(http.StatusNoContent)
}

func (h *Handler) Me(c *gin.Context) {
	p := principal(c)
	teams, err := h.Teams.List(c.Request.Context(), p)
	if err != nil {
		h.fail(c, err)
		return
	}
	type teamRole struct {
		*model.Team
		Role string `json:"role"`
	}
	out := make([]teamRole, 0, len(teams))
	for _, t := range teams {
		out = append(out, teamRole{Team: t, Role: p.TeamRole(t.ID)})
	}
	c.JSON(http.StatusOK, gin.H{"user": p.User, "teams": out})
}

func (h *Handler) UpdateMe(c *gin.Context) {
	var in struct {
		Name string `json:"name"`
	}
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	if err := h.Auth.UpdateProfile(c.Request.Context(), principal(c), in.Name); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ChangePassword(c *gin.Context) {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	if err := h.Auth.ChangePassword(c.Request.Context(), principal(c), in.Current, in.New); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Users ------------------------------------------------------------------------------------

func (h *Handler) ListUsers(c *gin.Context) {
	if err := principal(c).RequireAdmin(); err != nil {
		h.fail(c, err)
		return
	}
	users, err := h.Repo.ListUsers(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": users})
}

func (h *Handler) CreateUser(c *gin.Context) {
	var in service.CreateUserInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	u, err := h.Auth.CreateUser(c.Request.Context(), principal(c), in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, u)
}

func (h *Handler) UpdateUser(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	var in service.UpdateUserInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	if err := h.Auth.UpdateUser(c.Request.Context(), principal(c), id, in); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Teams ------------------------------------------------------------------------------------

func (h *Handler) ListTeams(c *gin.Context) {
	teams, err := h.Teams.List(c.Request.Context(), principal(c))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": teams})
}

func (h *Handler) CreateTeam(c *gin.Context) {
	var in service.TeamInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	t, err := h.Teams.Create(c.Request.Context(), principal(c), in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, t)
}

func (h *Handler) GetTeam(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	t, err := h.Teams.Get(c.Request.Context(), principal(c), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"team": t, "role": principal(c).TeamRole(id)})
}

func (h *Handler) UpdateTeam(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	var in service.TeamInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	t, err := h.Teams.Update(c.Request.Context(), principal(c), id, in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, t)
}

func (h *Handler) ListMembers(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	ms, err := h.Teams.Members(c.Request.Context(), principal(c), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ms})
}

func (h *Handler) AddMember(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	var in service.MemberInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	if err := h.Teams.AddMember(c.Request.Context(), principal(c), id, in); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) RemoveMember(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	uid, err := paramID(c, "userId")
	if err != nil {
		h.fail(c, err)
		return
	}
	if err := h.Teams.RemoveMember(c.Request.Context(), principal(c), id, uid); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) GetBudget(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	st, err := h.Teams.Budget(c.Request.Context(), principal(c), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, st)
}

func (h *Handler) SetBudget(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	var in service.BudgetInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	st, err := h.Teams.SetBudget(c.Request.Context(), principal(c), id, in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, st)
}

func (h *Handler) ListKeys(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	keys, err := h.Teams.Keys(c.Request.Context(), principal(c), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": keys})
}

func (h *Handler) CreateKey(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	var in service.KeyInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	k, err := h.Teams.CreateKey(c.Request.Context(), principal(c), id, in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, k)
}

func (h *Handler) RevokeKey(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	kid, err := paramID(c, "keyId")
	if err != nil {
		h.fail(c, err)
		return
	}
	if err := h.Teams.RevokeKey(c.Request.Context(), principal(c), id, kid); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Providers, models, pricing, routing ---------------------------------------------------

func (h *Handler) ListProviders(c *gin.Context) {
	ps, err := h.Providers.List(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	snaps := h.Engine.Breakers().Snapshots()
	type withHealth struct {
		*model.Provider
		CircuitState string `json:"circuit_state"`
	}
	out := make([]withHealth, 0, len(ps))
	for _, p := range ps {
		st := "closed"
		if s, ok := snaps[p.ID.String()]; ok {
			st = s.State.String()
		}
		out = append(out, withHealth{Provider: p, CircuitState: st})
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

func (h *Handler) CreateProvider(c *gin.Context) {
	var in service.ProviderInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	p, err := h.Providers.Create(c.Request.Context(), principal(c), in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, p)
}

func (h *Handler) GetProvider(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	p, err := h.Providers.Get(c.Request.Context(), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	snap := h.Engine.Breakers().Get(id.String()).Snapshot()
	c.JSON(http.StatusOK, gin.H{"provider": p, "circuit": circuitJSON(snap.State.String(), snap.ConsecutiveFailures, snap.OpenedAt, snap.LastFailureAt, snap.LastError)})
}

func (h *Handler) UpdateProvider(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	var in service.ProviderInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	p, err := h.Providers.Update(c.Request.Context(), principal(c), id, in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

func (h *Handler) DeleteProvider(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	if err := h.Providers.Delete(c.Request.Context(), principal(c), id); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ListModelsAdmin(c *gin.Context) {
	pid, err := queryUUID(c, "provider_id")
	if err != nil {
		h.fail(c, err)
		return
	}
	ms, err := h.Providers.Models(c.Request.Context(), pid)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ms})
}

func (h *Handler) CreateModel(c *gin.Context) {
	var in service.ModelInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	m, err := h.Providers.CreateModel(c.Request.Context(), principal(c), in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, m)
}

func (h *Handler) UpdateModel(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	var in service.ModelInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	m, err := h.Providers.UpdateModel(c.Request.Context(), principal(c), id, in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, m)
}

func (h *Handler) DeleteModel(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	if err := h.Providers.DeleteModel(c.Request.Context(), principal(c), id); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ListPrices(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	ps, err := h.Providers.Prices(c.Request.Context(), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ps})
}

func (h *Handler) AddPrice(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	var in service.PriceInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	p, err := h.Providers.AddPrice(c.Request.Context(), principal(c), id, in)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, p)
}

func (h *Handler) ListPolicies(c *gin.Context) {
	ps, err := h.Providers.Policies(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ps})
}

func (h *Handler) SavePolicy(c *gin.Context) {
	var id *uuid.UUID
	if c.Param("id") != "" {
		pid, err := paramID(c, "id")
		if err != nil {
			h.fail(c, err)
			return
		}
		id = &pid
	}
	var in service.PolicyInput
	if err := bind(c, &in); err != nil {
		h.fail(c, err)
		return
	}
	p, err := h.Providers.SavePolicy(c.Request.Context(), principal(c), id, in)
	if err != nil {
		h.fail(c, err)
		return
	}
	status := http.StatusOK
	if id == nil {
		status = http.StatusCreated
	}
	c.JSON(status, p)
}

func (h *Handler) DeletePolicy(c *gin.Context) {
	id, err := paramID(c, "id")
	if err != nil {
		h.fail(c, err)
		return
	}
	if err := h.Providers.DeletePolicy(c.Request.Context(), principal(c), id); err != nil {
		h.fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Audit & settings ----------------------------------------------------------------------

func (h *Handler) ListAudit(c *gin.Context) {
	if err := principal(c).RequireAdmin(); err != nil {
		h.fail(c, err)
		return
	}
	logs, err := h.Repo.ListAudit(c.Request.Context(), queryInt(c, "limit", 50), queryInt(c, "offset", 0))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": logs})
}

// Settings exposes non-secret runtime configuration for the console.
func (h *Handler) Settings(c *gin.Context) {
	cfg := h.Cfg
	c.JSON(http.StatusOK, gin.H{
		"environment":               cfg.Env,
		"default_team_rpm":          cfg.DefaultTeamRPM,
		"default_team_concurrency":  cfg.DefaultTeamConcurrency,
		"default_max_output_tokens": cfg.DefaultMaxOutputTokens,
		"request_timeout_ms":        cfg.RequestTimeout.Milliseconds(),
		"stream_timeout_ms":         cfg.StreamTimeout.Milliseconds(),
		"reservation_ttl_ms":        cfg.ReservationTTL.Milliseconds(),
		"breaker_failure_threshold": cfg.BreakerFailureThreshold,
		"breaker_open_duration_ms":  cfg.BreakerOpenDuration.Milliseconds(),
		"rate_limit_fail_open":      cfg.RateLimitFailOpen,
		"tracing_enabled":           cfg.OTLPEndpoint != "",
		"currency":                  "USD",
	})
}
