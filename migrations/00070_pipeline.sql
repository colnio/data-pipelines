-- +goose Up
-- Pipeline artefacts: parser results, analysis outputs, review artefacts, and
-- review decisions (architecture §17). All reference runs(id) with CASCADE so
-- a dropped run cleans up its pipeline rows.

-- Parser result for one parse attempt; status distinguishes success/failure/warning.
CREATE TABLE parser_results (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id           text        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    parser_version   text,
    status           text,
    columns_expected int,
    rows_declared    int,
    rows_actual      int,
    warnings_json    jsonb       NOT NULL DEFAULT '[]',
    output_json      jsonb       NOT NULL DEFAULT '{}',
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX parser_results_run_idx        ON parser_results (run_id);
CREATE INDEX parser_results_created_at_idx ON parser_results (created_at DESC);

-- Versioned analysis output artefacts (plots, computed files, etc.).
CREATE TABLE analysis_artifacts (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id           text        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    analysis_version int         NOT NULL DEFAULT 1,
    kind             text,
    path             text,
    sha256           text,
    meta_json        jsonb       NOT NULL DEFAULT '{}',
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX analysis_artifacts_run_idx        ON analysis_artifacts (run_id);
CREATE INDEX analysis_artifacts_created_at_idx ON analysis_artifacts (created_at DESC);

-- Review artefact produced by Pipeline A; one per processing attempt.
CREATE TABLE review_artifacts (
    id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id                text        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    version               int         NOT NULL DEFAULT 1,
    summary_json          jsonb       NOT NULL DEFAULT '{}',
    plots_json            jsonb       NOT NULL DEFAULT '[]',
    metrics_json          jsonb       NOT NULL DEFAULT '{}',
    parser_warnings_json  jsonb       NOT NULL DEFAULT '[]',
    llm_summary           text        NOT NULL DEFAULT '',
    created_at            timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX review_artifacts_run_idx        ON review_artifacts (run_id);
CREATE INDEX review_artifacts_created_at_idx ON review_artifacts (created_at DESC);

-- Human review decisions: approve, request_changes, quarantine, or supersede.
CREATE TABLE review_decisions (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id              text        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    review_artifact_id  bigint      REFERENCES review_artifacts(id),
    decision            text        CHECK (decision IN ('approve','request_changes','quarantine','supersede')),
    reviewer            text,
    reason              text        NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX review_decisions_run_idx        ON review_decisions (run_id);
CREATE INDEX review_decisions_created_at_idx ON review_decisions (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS review_decisions;
DROP TABLE IF EXISTS review_artifacts;
DROP TABLE IF EXISTS analysis_artifacts;
DROP TABLE IF EXISTS parser_results;
