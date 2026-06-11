-- +goose Up
-- A run is one declared measurement event (architecture §7, §14). Structural
-- truth (sample/device/contact_config) is referenced by bare text ids — there
-- is intentionally NO foreign key to the catalog tables so the catalog can be
-- registered/corrected independently and a run can be ingested before its
-- metadata is complete (the needs_metadata holding state, §10).
--
-- runs.id accepts a uuid or snowflake string declared by the agent. state is
-- the run lifecycle; the CHECK lists every state in internal/domain/state.go.
-- Only the statemachine package may UPDATE runs.state (§16).
CREATE TABLE runs (
    id                text        PRIMARY KEY,
    manifest_hash     text        NOT NULL,
    agent_id          text        NOT NULL REFERENCES agents(id),
    measurement_type  text        NOT NULL DEFAULT '',
    completion_source text        NOT NULL CHECK (completion_source IN ('instrument', 'operator')),
    sample_id         text,
    device_id         text,
    contact_config_id text,
    meas_path         text        NOT NULL,
    operator_comment  text        NOT NULL DEFAULT '',
    state             text        NOT NULL DEFAULT 'declared' CHECK (state IN (
        'declared','pulling','unpacked','verified','promoted','needs_metadata',
        'parsing','validated','processing','awaiting_review','approved',
        'publishing','published','changes_requested',
        'transfer_failed','manifest_mismatch','unsafe_archive','parser_failed',
        'quarantined','processing_failed','review_rejected'
    )),
    declared_by       text        NOT NULL DEFAULT '',
    declared_at       timestamptz NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX runs_state_idx        ON runs (state);
CREATE INDEX runs_agent_idx        ON runs (agent_id);
CREATE INDEX runs_sample_idx       ON runs (sample_id);
CREATE INDEX runs_device_idx       ON runs (device_id);
CREATE INDEX runs_declared_at_idx  ON runs (declared_at DESC);

-- Verified raw files belonging to a run. sha256 is authoritative for the final
-- stored raw data (architecture §13 "two hash layers").
CREATE TABLE run_files (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id     text        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    name       text        NOT NULL,
    bytes      bigint      NOT NULL,
    sha256     text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, name)
);

CREATE INDEX run_files_run_idx ON run_files (run_id);

-- The manifest as received (architecture §11). UNIQUE(run_id, manifest_hash)
-- makes manifest POSTs idempotent: a duplicate notification with the same hash
-- conflicts harmlessly. If an instrument manifest later supersedes an operator
-- snapshot they coexist as distinct hashes and the discrepancy is audited (§14).
CREATE TABLE manifests (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id            text        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    manifest_hash     text        NOT NULL,
    schema_version    int         NOT NULL,
    completion_source text        NOT NULL,
    declared_by       text        NOT NULL DEFAULT '',
    declared_at       timestamptz NOT NULL,
    raw_json          jsonb       NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, manifest_hash)
);

-- Per-run, machine-generated instrument JSON: immutable, hashed, transferred
-- with the raw data (architecture §7). Not remodelled into operator columns.
CREATE TABLE instrument_metadata_raw (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id     text        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    filename   text        NOT NULL,
    sha256     text        NOT NULL,
    raw_json   jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, filename)
);

-- Append-only audit log of every run state change (architecture §16).
CREATE TABLE run_state_transitions (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id       text        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    from_state   text        NOT NULL,
    to_state     text        NOT NULL,
    actor_type   text        NOT NULL CHECK (actor_type IN ('agent','worker','api','reviewer','system')),
    actor_id     text        NOT NULL DEFAULT '',
    reason       text        NOT NULL DEFAULT '',
    payload_json jsonb       NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX run_state_transitions_run_idx ON run_state_transitions (run_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS run_state_transitions;
DROP TABLE IF EXISTS instrument_metadata_raw;
DROP TABLE IF EXISTS manifests;
DROP TABLE IF EXISTS run_files;
DROP TABLE IF EXISTS runs;
