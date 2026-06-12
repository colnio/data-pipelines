// Command server is the lab-data API entrypoint. It loads configuration, opens
// the Postgres pool, runs goose migrations behind an advisory lock, wires the
// chi+huma server plus domain modules, and starts listening.
//
// Subagents must NOT edit this file: domain modules expose Register/NewService
// and the orchestrator wires them here (per AGENTS.md, avoids merge conflicts
// during parallel work).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/colnio/data-pipelines/internal/admin"
	"github.com/colnio/data-pipelines/internal/agentauth"
	"github.com/colnio/data-pipelines/internal/auth"
	"github.com/colnio/data-pipelines/internal/catalog"
	"github.com/colnio/data-pipelines/internal/config"
	"github.com/colnio/data-pipelines/internal/db"
	"github.com/colnio/data-pipelines/internal/fileserve"
	"github.com/colnio/data-pipelines/internal/ingest"
	"github.com/colnio/data-pipelines/internal/jobs"
	"github.com/colnio/data-pipelines/internal/jupyter"
	"github.com/colnio/data-pipelines/internal/notify"
	"github.com/colnio/data-pipelines/internal/platform"
	"github.com/colnio/data-pipelines/internal/review"
	"github.com/colnio/data-pipelines/internal/run"
)

func main() {
	if err := runServer(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func runServer() error {
	_ = godotenv.Load() // .env is optional; real env wins.

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	logger.Info("running migrations")
	if err := db.Migrate(ctx, pool, cfg.DatabaseURL); err != nil {
		return err
	}

	// Human auth (reviewers) — provides the platform Verifier and JWT endpoints.
	authSvc, err := auth.NewService(pool, auth.Config{
		JWTSigningKey:       cfg.JWTSigningKey,
		AccessTokenTTL:      cfg.AccessTokenTTL,
		AllowedEmailDomains: cfg.AllowedEmailDomains,
		IsProduction:        cfg.IsProduction(),
	}, logger)
	if err != nil {
		return err
	}

	srv := platform.New(&platform.ServerDeps{
		Logger:      logger,
		WebOrigin:   cfg.WebOrigin,
		Verifier:    authSvc,
		Idempotency: platform.NewIdempotencyStore(pool),
		Production:  cfg.IsProduction(),
		RateLimiter: platform.NewRateLimiterFromPool(pool, 600),
	})

	// ── Domain modules ───────────────────────────────────────────────────────
	runRepo := run.NewRepo(pool)
	agentSvc := agentauth.NewService(pool)
	queue := jobs.NewQueue(pool)
	ingestSvc := ingest.NewService(pool, agentSvc, runRepo, queue, logger)
	reviewSvc := review.NewService(pool, runRepo, queue, logger)

	auth.Register(srv.API, authSvc)
	run.Register(srv.API, runRepo)
	ingest.Register(srv.API, ingestSvc)
	review.Register(srv.API, reviewSvc)

	// ── Browse, admin, integrations (added with the key-functionality work) ──
	catalog.Register(srv.API, catalog.NewService(pool))
	admin.Register(srv.API, admin.NewService(pool, authSvc, agentSvc))
	notify.Register(srv.API, notify.NewService(pool, cfg, queue, logger))
	jupyter.Register(srv.API, jupyter.NewService(cfg, logger))
	// fileserve streams binary artifacts/raw files; it mounts raw chi routes
	// (not huma) on the same router so the AuthResolver middleware still runs.
	fileserve.Register(srv.Router, fileserve.NewService(pool, runRepo, cfg))

	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("listening", "addr", httpSrv.Addr, "env", cfg.Env)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}
