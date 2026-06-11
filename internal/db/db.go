// Package db owns the Postgres connection pool and the migration runner.
// sqlc-generated query code also lives under this package tree.
package db

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect opens a pgx pool against the given DSN and verifies connectivity.
// Pool sizing and health checks are configured via DB_MAX_CONNS and
// DB_HEALTH_CHECK_PERIOD (Go duration string, e.g. "30s"); zero values leave
// pgx defaults.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	applyPoolEnv(cfg)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func applyPoolEnv(cfg *pgxpool.Config) {
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxConns = int32(n)
		}
	}
	if v := os.Getenv("DB_HEALTH_CHECK_PERIOD"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.HealthCheckPeriod = d
		}
	}
}
