// Package router wires HTTP routes and middleware chains for both planes.
package router

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"service/internal/handler"
	"service/internal/observability"
)

const (
	dataPlaneBodyLimit    = 1 << 20   // 1 MiB
	controlPlaneBodyLimit = 256 << 10 // 256 KiB
)

func New(h *handler.Handler, m *observability.Metrics, log *slog.Logger, trustedProxies []string) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.HandleMethodNotAllowed = true
	if err := r.SetTrustedProxies(trustedProxies); err != nil {
		log.Error("invalid TRUSTED_PROXIES", "error", err)
	}
	r.Use(observability.Recover(log), observability.RequestID(), observability.AccessLog(log, m), observability.Trace(), observability.SecurityHeaders())

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"type": "invalid_request_error", "code": "not_found", "message": "route not found"}})
	})
	r.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": gin.H{"type": "invalid_request_error", "code": "method_not_allowed", "message": "method not allowed"}})
	})

	r.GET("/healthz", h.Healthz)
	r.GET("/readyz", h.Readyz)
	r.GET("/metrics", gin.WrapH(promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})))

	// Data plane.
	v1 := r.Group("/v1", observability.BodyLimit(dataPlaneBodyLimit), h.RequireAPIKey())
	v1.POST("/chat/completions", h.ChatCompletions)
	v1.GET("/models", h.ListModels)

	// Control plane.
	api := r.Group("/api/v1", observability.BodyLimit(controlPlaneBodyLimit))
	api.POST("/auth/login", h.Login)

	authed := api.Group("", h.RequireSession())
	authed.POST("/auth/logout", h.Logout)
	authed.GET("/auth/me", h.Me)
	authed.PATCH("/auth/me", h.UpdateMe)
	authed.POST("/auth/password", h.ChangePassword)

	authed.GET("/users", h.ListUsers)
	authed.POST("/users", h.CreateUser)
	authed.PATCH("/users/:id", h.UpdateUser)

	authed.GET("/teams", h.ListTeams)
	authed.POST("/teams", h.CreateTeam)
	authed.GET("/teams/:id", h.GetTeam)
	authed.PATCH("/teams/:id", h.UpdateTeam)
	authed.GET("/teams/:id/members", h.ListMembers)
	authed.POST("/teams/:id/members", h.AddMember)
	authed.DELETE("/teams/:id/members/:userId", h.RemoveMember)
	authed.GET("/teams/:id/budget", h.GetBudget)
	authed.PUT("/teams/:id/budget", h.SetBudget)
	authed.GET("/teams/:id/api-keys", h.ListKeys)
	authed.POST("/teams/:id/api-keys", h.CreateKey)
	authed.DELETE("/teams/:id/api-keys/:keyId", h.RevokeKey)

	authed.GET("/providers", h.ListProviders)
	authed.POST("/providers", h.CreateProvider)
	authed.GET("/providers/:id", h.GetProvider)
	authed.PATCH("/providers/:id", h.UpdateProvider)
	authed.DELETE("/providers/:id", h.DeleteProvider)

	authed.GET("/models", h.ListModelsAdmin)
	authed.POST("/models", h.CreateModel)
	authed.PATCH("/models/:id", h.UpdateModel)
	authed.DELETE("/models/:id", h.DeleteModel)
	authed.GET("/models/:id/prices", h.ListPrices)
	authed.POST("/models/:id/prices", h.AddPrice)

	authed.GET("/routing-policies", h.ListPolicies)
	authed.POST("/routing-policies", h.SavePolicy)
	authed.PUT("/routing-policies/:id", h.SavePolicy)
	authed.DELETE("/routing-policies/:id", h.DeletePolicy)

	authed.GET("/analytics/summary", h.AnalyticsSummary)
	authed.GET("/analytics/timeseries", h.AnalyticsTimeseries)
	authed.GET("/analytics/breakdown", h.AnalyticsBreakdown)
	authed.GET("/requests", h.ListRequests)
	authed.GET("/requests/:id", h.GetRequest)
	authed.GET("/reliability", h.Reliability)
	authed.POST("/reliability/breakers/:id/reset", h.ResetBreaker)
	authed.GET("/audit-logs", h.ListAudit)
	authed.GET("/settings", h.Settings)

	// Playground uses the data-plane body limit (prompts can be large).
	pg := r.Group("/api/v1/playground", observability.BodyLimit(dataPlaneBodyLimit), h.RequireSession())
	pg.POST("/chat", h.Playground)

	return r
}
