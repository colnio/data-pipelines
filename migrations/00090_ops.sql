-- +goose Up
-- Operational tables: LLM audit records, notifications, backup records, and users
-- (architecture §20 §21 §6 + human auth). reviewer/published_by/performed_by are
-- stored as bare text — no table FKs to users, keeping modules independent.

-- Audit record for every LLM call; the LLM is advisory and never auto-approves.
CREATE TABLE llm_records (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id         text        REFERENCES runs(id) ON DELETE CASCADE,
    model          text,
    provider       text,
    prompt_version text,
    input_policy   text,
    input_hash     text,
    output_hash    text,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX llm_records_run_idx        ON llm_records (run_id);
CREATE INDEX llm_records_created_at_idx ON llm_records (created_at DESC);

-- Outbound notification records; status tracks delivery lifecycle.
CREATE TABLE notifications (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_type   text,
    channel      text,
    target       text,
    payload_json jsonb       NOT NULL DEFAULT '{}',
    status       text        NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','sent','failed','skipped')),
    last_error   text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    sent_at      timestamptz
);

CREATE INDEX notifications_status_idx     ON notifications (status);
CREATE INDEX notifications_created_at_idx ON notifications (created_at DESC);

-- Backup operation records; verified_at is NULL until a restore test confirms.
CREATE TABLE backup_records (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id      text,
    scope       text,
    status      text,
    location    text,
    checksum    text,
    verified_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX backup_records_run_idx        ON backup_records (run_id);
CREATE INDEX backup_records_created_at_idx ON backup_records (created_at DESC);

-- Human user accounts. global_role is coarse-grained; fine-grained permissions
-- are enforced by the API layer. reviewer/published_by/performed_by in other
-- tables are bare text — no FK here — so modules stay independent.
CREATE TABLE users (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text        UNIQUE NOT NULL,
    password_hash text        NOT NULL DEFAULT '',
    display_name  text        NOT NULL DEFAULT '',
    global_role   text        NOT NULL DEFAULT 'member'
                      CHECK (global_role IN ('admin','pi','member')),
    status        text        NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending','active','disabled')),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX users_email_idx      ON users (email);
CREATE INDEX users_created_at_idx ON users (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS backup_records;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS llm_records;
