package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/colnio/data-pipelines/migrations"
)

// advisoryLockKey is a fixed key so concurrent boots serialize migrations.
const advisoryLockKey int64 = 4711_2026

// Migrate runs all pending goose migrations from the embedded FS. It holds a
// Postgres advisory lock for the duration so multiple booting instances do not
// race. It is idempotent.
func Migrate(ctx context.Context, pool *pgxpool.Pool, dsn string) error {
	// goose needs a database/sql handle; open one over the same pgx driver.
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	if err := withAdvisoryLock(ctx, sqlDB, func() error {
		goose.SetBaseFS(migrations.FS)
		if err := goose.SetDialect("postgres"); err != nil {
			return fmt.Errorf("set dialect: %w", err)
		}
		if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
			return fmt.Errorf("goose up: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func withAdvisoryLock(ctx context.Context, sqlDB *sql.DB, fn func() error) error {
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn for lock: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()

	return fn()
}
