-- +goose Up
-- Write-replay protection (platform infra). Composite PK on (principal_key, key);
-- rows expire after 24h (GC'd by a periodic job). response_body stores the
-- captured JSON so a retried request replays the original response verbatim.
CREATE TABLE idempotency_keys (
    principal_key   text        NOT NULL,
    key             text        NOT NULL,
    response_status int         NOT NULL,
    response_body   bytea       NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (principal_key, key)
);
CREATE INDEX idempotency_keys_created_at_idx ON idempotency_keys (created_at);

-- +goose Down
DROP TABLE idempotency_keys;
