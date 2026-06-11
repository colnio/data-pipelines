package platform

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// rateLimitBackend records and checks sliding-window hits for a bucket key.
type rateLimitBackend interface {
	allow(ctx context.Context, key string, perMinute int) (bool, error)
}

// memRateLimitBackend is an in-process sliding window (tests / fallback).
type memRateLimitBackend struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

// pgRateLimitBackend persists hits in Postgres for multi-instance deployments.
type pgRateLimitBackend struct {
	pool *pgxpool.Pool
}

func newMemRateLimitBackend() *memRateLimitBackend {
	return &memRateLimitBackend{hits: make(map[string][]time.Time)}
}

func (b *memRateLimitBackend) allow(_ context.Context, key string, perMinute int) (bool, error) {
	if perMinute <= 0 {
		return true, nil
	}
	now := time.Now()
	cutoff := now.Add(-time.Minute)

	b.mu.Lock()
	defer b.mu.Unlock()

	times := b.hits[key]
	kept := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= perMinute {
		b.hits[key] = kept
		return false, nil
	}
	b.hits[key] = append(kept, now)
	return true, nil
}

func newPgRateLimitBackend(pool *pgxpool.Pool) *pgRateLimitBackend {
	return &pgRateLimitBackend{pool: pool}
}

func (b *pgRateLimitBackend) allow(ctx context.Context, key string, perMinute int) (bool, error) {
	if perMinute <= 0 {
		return true, nil
	}
	// Best-effort eviction of stale rows (2-minute TTL).
	_, _ = b.pool.Exec(ctx,
		`DELETE FROM rate_limit_hits WHERE hit_at < now() - interval '2 minutes'`)

	var count int
	err := b.pool.QueryRow(ctx,
		`SELECT COUNT(*)::int FROM rate_limit_hits
		 WHERE bucket_key = $1 AND hit_at > now() - interval '1 minute'`,
		key,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("rate limit count: %w", err)
	}
	if count >= perMinute {
		return false, nil
	}
	_, err = b.pool.Exec(ctx,
		`INSERT INTO rate_limit_hits (bucket_key) VALUES ($1)`,
		key,
	)
	if err != nil {
		return false, fmt.Errorf("rate limit insert: %w", err)
	}
	return true, nil
}
