package platform

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgIdempotencyStore is the Postgres-backed IdempotencyStore. Rows older than
// 24h are removed by a nightly GC job (River); this store only reads/writes.
type PgIdempotencyStore struct {
	pool *pgxpool.Pool
}

func NewIdempotencyStore(pool *pgxpool.Pool) *PgIdempotencyStore {
	return &PgIdempotencyStore{pool: pool}
}

var _ IdempotencyStore = (*PgIdempotencyStore)(nil)

func (s *PgIdempotencyStore) Lookup(ctx context.Context, principalKey, key string) (int, []byte, bool, error) {
	var status int
	var body []byte
	err := s.pool.QueryRow(ctx,
		`SELECT response_status, response_body FROM idempotency_keys
		 WHERE principal_key = $1 AND key = $2 AND created_at > now() - interval '24 hours'`,
		principalKey, key,
	).Scan(&status, &body)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, false, nil
		}
		return 0, nil, false, err
	}
	return status, body, true, nil
}

func (s *PgIdempotencyStore) Save(ctx context.Context, principalKey, key string, status int, body []byte) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO idempotency_keys (principal_key, key, response_status, response_body)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (principal_key, key) DO NOTHING`,
		principalKey, key, status, body,
	)
	return err
}
