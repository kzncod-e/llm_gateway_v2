// Package app wires all dependencies into a runnable gateway. It is shared by
// cmd/api and the end-to-end tests so both exercise the same object graph.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"service/internal/breaker"
	"service/internal/budget"
	"service/internal/config"
	"service/internal/gateway"
	"service/internal/handler"
	"service/internal/observability"
	"service/internal/provider"
	"service/internal/provider/anthropic"
	"service/internal/provider/gemini"
	"service/internal/provider/openai"
	"service/internal/ratelimit"
	"service/internal/repository"
	"service/internal/router"
	"service/internal/security"
	"service/internal/seed"
	"service/internal/service"
	"service/internal/usage"
)

type App struct {
	Handler  http.Handler
	Engine   *gateway.Engine
	Catalog  *gateway.Catalog
	Budgets  *budget.Service
	Repo     *repository.Repo
	Metrics  *observability.Metrics
	Breakers *breaker.Registry
}

// New builds the application and loads the routing catalog.
func New(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, rdb redis.UniversalClient, log *slog.Logger) (*App, error) {
	box, err := security.NewSecretBox(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	hasher := security.NewKeyHasher(cfg.APIKeyPepper)
	repo := repository.New(pool)
	metrics := observability.NewMetrics()
	audit := service.NewAuditor(repo, log)

	breakers := breaker.NewRegistry(breaker.Settings{FailureThreshold: cfg.BreakerFailureThreshold, OpenDuration: cfg.BreakerOpenDuration, HalfOpenMax: 1}, nil,
		func(key string, from, to breaker.State) {
			log.Warn("circuit breaker transition", "provider_id", key, "from", from.String(), "to", to.String())
		})

	var providers *service.ProviderService
	catalog := gateway.NewCatalog(func(ctx context.Context) (*gateway.Snapshot, error) { return providers.LoadCatalog(ctx) }, log)
	providers = service.NewProviderService(repo, audit, box, catalog, log)

	limiter := ratelimit.New(rdb, cfg.RateLimitFailOpen)
	budgets := budget.New(pool, cfg.ReservationTTL)
	httpClient := &http.Client{Transport: otelhttp.NewTransport(provider.DefaultHTTPClient.Transport)}
	engine := gateway.NewEngine(gateway.Deps{
		Catalog:    catalog,
		Adapters:   []provider.Adapter{openai.New(), anthropic.New(), gemini.New()},
		Breakers:   breakers,
		Limiter:    limiter,
		Budget:     budgets,
		Recorder:   usage.NewRecorder(pool),
		Observer:   metrics,
		Logger:     log,
		HTTPClient: httpClient,
	}, gateway.Options{DefaultMaxTokens: cfg.DefaultMaxOutputTokens, RequestTimeout: cfg.RequestTimeout, StreamTimeout: cfg.StreamTimeout})

	auth := service.NewAuthService(repo, audit, limiter, cfg.SessionTTL, log)
	keys := service.NewKeyAuth(repo, hasher, log)
	teams := service.NewTeamService(repo, audit, keys, hasher, budgets)

	if created, err := auth.BootstrapAdmin(ctx, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		return nil, fmt.Errorf("bootstrap admin: %w", err)
	} else if created {
		log.Info("bootstrap administrator created", "email", cfg.AdminEmail)
	}
	if cfg.SeedDemo {
		if err := seed.Demo(ctx, repo, box, hasher, seed.Options{MockBaseURL: cfg.MockBaseURL, DemoAPIKey: cfg.DemoAPIKey, AdminEmail: cfg.AdminEmail}, log); err != nil {
			return nil, fmt.Errorf("seed demo data: %w", err)
		}
	}
	if err := catalog.Reload(ctx); err != nil {
		return nil, fmt.Errorf("load catalog: %w", err)
	}

	h := &handler.Handler{Cfg: cfg, Log: log, Repo: repo, Engine: engine, Auth: auth, Keys: keys, Teams: teams, Providers: providers, Budgets: budgets, Limiter: limiter}
	return &App{
		Handler: router.New(h, metrics, log, cfg.TrustedProxies), Engine: engine, Catalog: catalog,
		Budgets: budgets, Repo: repo, Metrics: metrics, Breakers: breakers,
	}, nil
}

// RunBackground starts periodic jobs until ctx is cancelled.
func (a *App) RunBackground(ctx context.Context, cfg *config.Config, log *slog.Logger) {
	go a.Catalog.Run(ctx, cfg.CatalogRefresh)
	go every(ctx, time.Minute, func(ctx context.Context) {
		if n, err := a.Budgets.SweepExpired(ctx); err != nil {
			log.Error("reservation sweep failed", "error", err)
		} else if n > 0 {
			log.Warn("released expired budget reservations", "count", n)
		}
	})
	go every(ctx, time.Hour, func(ctx context.Context) {
		if _, err := a.Repo.DeleteExpiredSessions(ctx); err != nil {
			log.Error("session cleanup failed", "error", err)
		}
	})
}

func every(ctx context.Context, d time.Duration, fn func(context.Context)) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c, cancel := context.WithTimeout(ctx, 30*time.Second)
			fn(c)
			cancel()
		}
	}
}
