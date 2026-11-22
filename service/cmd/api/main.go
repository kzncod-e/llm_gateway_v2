// Command api runs the LLM gateway (data plane + control plane).
//
//	api serve    start the HTTP server (default)
//	api migrate  apply database migrations and exit
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"service/internal/breaker"
	"service/internal/budget"
	"service/internal/config"
	"service/internal/database"
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

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	if cmd == "healthcheck" { // container health probe (the image has no shell or curl)
		os.Exit(healthcheck())
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	log := observability.NewLogger(cfg.LogLevel)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "serve":
		err = serve(ctx, cfg, log)
	case "migrate":
		err = migrateOnly(ctx, cfg, log)
	default:
		err = fmt.Errorf("unknown command %q (use serve or migrate)", cmd)
	}
	if err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func migrateOnly(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	pool, err := database.Connect(ctx, cfg.DatabaseURL, nil)
	if err != nil {
		return err
	}
	defer pool.Close()
	return database.Migrate(ctx, pool, log)
}

func serve(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	shutdownTracing, err := observability.SetupTracing(ctx, cfg.OTLPEndpoint, cfg.ServiceName, log)
	if err != nil {
		return fmt.Errorf("setup tracing: %w", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(sctx); err != nil {
			log.Warn("tracing shutdown", "error", err)
		}
	}()

	pool, err := database.Connect(ctx, cfg.DatabaseURL, observability.NewPgxTracer())
	if err != nil {
		return err
	}
	defer pool.Close()
	if cfg.AutoMigrate {
		if err := database.Migrate(ctx, pool, log); err != nil {
			return err
		}
	}

	ropts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("parse REDIS_URL: %w", err)
	}
	ropts.DialTimeout, ropts.ReadTimeout, ropts.WriteTimeout = 2*time.Second, time.Second, time.Second
	rdb := redis.NewClient(ropts)
	defer rdb.Close()
	if err := redisotel.InstrumentTracing(rdb); err != nil {
		log.Warn("redis tracing", "error", err)
	}

	box, err := security.NewSecretBox(cfg.EncryptionKey)
	if err != nil {
		return err
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
		return fmt.Errorf("bootstrap admin: %w", err)
	} else if created {
		log.Info("bootstrap administrator created", "email", cfg.AdminEmail)
	}
	if cfg.SeedDemo {
		if err := seed.Demo(ctx, repo, box, hasher, seed.Options{MockBaseURL: cfg.MockBaseURL, DemoAPIKey: cfg.DemoAPIKey, AdminEmail: cfg.AdminEmail}, log); err != nil {
			return fmt.Errorf("seed demo data: %w", err)
		}
	}
	if err := catalog.Reload(ctx); err != nil {
		return fmt.Errorf("load catalog: %w", err)
	}

	bg, cancelBG := context.WithCancel(context.Background())
	defer cancelBG()
	go catalog.Run(bg, cfg.CatalogRefresh)
	go every(bg, time.Minute, func(ctx context.Context) {
		if n, err := budgets.SweepExpired(ctx); err != nil {
			log.Error("reservation sweep failed", "error", err)
		} else if n > 0 {
			log.Warn("released expired budget reservations", "count", n)
		}
	})
	go every(bg, time.Hour, func(ctx context.Context) {
		if _, err := repo.DeleteExpiredSessions(ctx); err != nil {
			log.Error("session cleanup failed", "error", err)
		}
	})

	h := &handler.Handler{Cfg: cfg, Log: log, Repo: repo, Engine: engine, Auth: auth, Keys: keys, Teams: teams, Providers: providers, Budgets: budgets, Limiter: limiter}
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router.New(h, metrics, log, cfg.TrustedProxies),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		// No ReadTimeout/WriteTimeout: they would cut long SSE streams. Bodies are
		// read under a per-request deadline and requests have engine deadlines.
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		log.Warn("graceful shutdown timed out, closing open connections", "error", err)
		_ = srv.Close()
	}
	dctx, dcancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer dcancel()
	if err := engine.Drain(dctx); err != nil {
		log.Error("pending usage writes not flushed before exit", "error", err)
	}
	return nil
}

func healthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" || addr[0] == ':' {
		addr = "127.0.0.1" + addr
		if addr == "127.0.0.1" {
			addr += ":8080"
		}
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
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
