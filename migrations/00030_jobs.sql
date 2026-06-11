-- +goose Up
-- The durable Postgres jobs queue (architecture §15). This table IS the
-- orchestrator. LISTEN/NOTIFY may wake workers, but polling must still work if
-- a notification is missed. Workers claim with FOR UPDATE SKIP LOCKED and a
-- crashed worker leaves a stale locked_until that another worker reclaims.
--
-- idempotency_key is UNIQUE so duplicate enqueues (e.g. a retried manifest POST
-- or a reconciliation scan re-driving a run) do not create duplicate work.
CREATE TABLE jobs (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_type        text        NOT NULL,
    run_id          text        REFERENCES runs(id) ON DELETE CASCADE,
    state           text        NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued','running','succeeded','failed','dead')),
    priority        int         NOT NULL DEFAULT 0,
    attempt_count   int         NOT NULL DEFAULT 0,
    max_attempts    int         NOT NULL DEFAULT 5,
    available_at    timestamptz NOT NULL DEFAULT now(),
    locked_by       text,
    locked_until    timestamptz,
    idempotency_key text        NOT NULL UNIQUE,
    payload_json    jsonb       NOT NULL DEFAULT '{}',
    result_json     jsonb,
    last_error      text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    finished_at     timestamptz
);

-- Claim query support: pick the highest-priority, oldest available queued job.
-- Partial index keeps it tight as succeeded/dead rows accumulate.
CREATE INDEX jobs_claim_idx ON jobs (priority DESC, created_at ASC)
    WHERE state = 'queued';
CREATE INDEX jobs_run_idx     ON jobs (run_id);
CREATE INDEX jobs_state_idx   ON jobs (state);
-- Reclaiming stale running jobs scans by locked_until.
CREATE INDEX jobs_locked_until_idx ON jobs (locked_until) WHERE state = 'running';

-- +goose Down
DROP TABLE IF EXISTS jobs;
