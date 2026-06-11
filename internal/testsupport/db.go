// Package testsupport provides shared helpers for tests that need a real
// Postgres (per the Definition of Done: HTTP contract tests run against a
// throwaway testdb, not mocks). Tests skip cleanly when Postgres is
// unreachable so `go test ./...` still passes in environments without Docker.
//
// The target database name is taken from TEST_DATABASE_URL when set. When it is
// NOT set, each test binary gets its OWN database (name derived from the test
// binary path) so packages running in parallel under a plain `go test ./...`
// never contend on the same tables. CI that pins one shared TEST_DATABASE_URL
// should run `go test ./... -p 1`.
package testsupport

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/colnio/data-pipelines/internal/db"
)

const adminDSN = "postgres://lab:lab@localhost:5432/lab?sslmode=disable"

var migrateOnce sync.Once

// DSN returns the test database DSN: TEST_DATABASE_URL when set, otherwise a
// per-test-binary database so parallel packages stay isolated.
func DSN() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(os.Args[0])) // distinct per test binary (per package)
	return fmt.Sprintf("postgres://lab:lab@localhost:5432/labdata_test_%08x?sslmode=disable", h.Sum32())
}

// NewPool returns a pool against a freshly-migrated test database. The first
// caller creates the target database (if missing) and runs all migrations once
// per process. If Postgres is unreachable, the test is skipped.
func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ensureTestDB(t, ctx)

	pool, err := db.Connect(ctx, DSN())
	if err != nil {
		t.Skipf("skipping: cannot connect to test database (%v)", err)
	}

	var migErr error
	migrateOnce.Do(func() {
		migErr = db.Migrate(ctx, pool, DSN())
	})
	if migErr != nil {
		t.Fatalf("migrate test db: %v", migErr)
	}
	t.Cleanup(pool.Close)
	return pool
}

func ensureTestDB(t *testing.T, ctx context.Context) {
	t.Helper()
	name, err := dbNameFromDSN(DSN())
	if err != nil || name == "" {
		t.Skipf("skipping: cannot parse test DSN (%v)", err)
	}
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Skipf("skipping: cannot reach Postgres (%v)", err)
	}
	defer admin.Close()
	if err := admin.Ping(ctx); err != nil {
		t.Skipf("skipping: Postgres not ready (%v)", err)
	}
	// CREATE DATABASE cannot run inside a transaction; ignore "already exists".
	// Name comes from a parsed DSN (identifier only), not user input.
	_, _ = admin.Exec(ctx, "CREATE DATABASE "+quoteIdent(name))
}

func dbNameFromDSN(dsn string) (string, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return "", err
	}
	return cfg.ConnConfig.Database, nil
}

func quoteIdent(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			out = append(out, '"')
		}
		out = append(out, s[i])
	}
	out = append(out, '"')
	return string(out)
}

// Truncate removes all rows from the given tables (RESTART IDENTITY CASCADE) so
// a test starts from a clean slate. Errors are ignored per-table so a table
// that does not exist yet (partial parallel build) is harmless.
func Truncate(t *testing.T, pool *pgxpool.Pool, tables ...string) {
	t.Helper()
	ctx := context.Background()
	for _, tbl := range tables {
		_, _ = pool.Exec(ctx, "TRUNCATE TABLE "+tbl+" RESTART IDENTITY CASCADE")
	}
}
