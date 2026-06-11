-- +goose Up
-- Sliding-window rate limit counters (platform infra) shared across API
-- replicas. Rows older than ~2 minutes are evicted on write; the bucket index
-- supports per-key counts.
CREATE TABLE rate_limit_hits (
    bucket_key text        NOT NULL,
    hit_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX rate_limit_hits_bucket_hit_at_idx ON rate_limit_hits (bucket_key, hit_at DESC);
CREATE INDEX rate_limit_hits_hit_at_idx ON rate_limit_hits (hit_at);

-- +goose Down
DROP TABLE rate_limit_hits;
