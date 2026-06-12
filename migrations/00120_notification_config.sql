-- +goose Up
-- Singleton table for Telegram notification configuration (architecture §21).
-- Enforced to at most one row via a unique index on the constant (true).
CREATE TABLE notification_config (
    id                  bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    bot_token           text        NOT NULL DEFAULT '',
    chat_id             text        NOT NULL DEFAULT '',
    on_awaiting_review  boolean     NOT NULL DEFAULT true,
    on_quarantined      boolean     NOT NULL DEFAULT true,
    on_published        boolean     NOT NULL DEFAULT true,
    on_test             boolean     NOT NULL DEFAULT true,
    updated_at          timestamptz NOT NULL DEFAULT now(),
    created_at          timestamptz NOT NULL DEFAULT now()
);

-- Enforce singleton: only one row is ever allowed.
CREATE UNIQUE INDEX notification_config_singleton ON notification_config ((true));

-- +goose Down
DROP TABLE IF EXISTS notification_config;
