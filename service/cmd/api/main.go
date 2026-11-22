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

	"service/internal/app"
	"service/internal/config"
	"service/internal/database"
	"service/internal/observability"
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

	application, err := app.New(ctx, cfg, pool, rdb, log)
	if err != nil {
		return err
	}
	bg, cancelBG := context.WithCancel(context.Background())
	defer cancelBG()
	application.RunBackground(bg, cfg, log)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           application.Handler,
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
	if err := application.Engine.Drain(dctx); err != nil {
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
