package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBTX is the minimal query surface shared by *pgxpool.Pool and pgx.Tx. Module
// methods accept a DBTX so the same code runs standalone (pool) or composed
// inside a caller-owned transaction (tx) — e.g. recording run_files in the same
// transaction that flips the run to 'promoted'.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
