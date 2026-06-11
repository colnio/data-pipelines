-- +goose Up
-- Publishing layer: processing environment records and versioned reproducibility
-- receipts (architecture §5). Published results are append-only and hashed.
-- A superseded_by uuid allows a chain of corrections without losing old receipts.

-- Records the Python/container environment used to process a run.
-- A Git SHA alone does not fully define a Python scientific environment.
CREATE TABLE processing_environments (
    id                              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    created_at                      timestamptz NOT NULL DEFAULT now(),
    python_version                  text,
    os_image_or_container_digest    text,
    lock_hash                       text,
    critical_packages_json          jsonb       NOT NULL DEFAULT '{}',
    docker_image_digest             text,
    entrypoint_command              text,
    notes                           text
);

CREATE INDEX processing_environments_created_at_idx ON processing_environments (created_at DESC);

-- Reproducibility receipt for each published result (architecture §5).
-- run_id is a text FK here because publication is tightly coupled to the run.
CREATE TABLE published_results (
    id                          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id                      text        REFERENCES runs(id),
    manifest_hash               text,
    raw_file_hashes_json        jsonb       NOT NULL DEFAULT '[]',
    parameter_version_ids_json  jsonb       NOT NULL DEFAULT '{}',
    processing_git_sha          text,
    processing_dirty_tree       boolean     NOT NULL DEFAULT false,
    processing_environment_id   bigint      REFERENCES processing_environments(id),
    processing_command          text,
    parser_version              text,
    instrument_software_version text,
    random_seed                 text,
    review_decision_id          bigint      REFERENCES review_decisions(id),
    superseded_by               uuid,
    published_by                text,
    published_at                timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX published_results_run_idx          ON published_results (run_id);
CREATE INDEX published_results_published_at_idx ON published_results (published_at DESC);

-- Output artefacts belonging to a published result; hashed and sized.
CREATE TABLE published_artifacts (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    published_result_id  uuid        NOT NULL REFERENCES published_results(id) ON DELETE CASCADE,
    path                 text,
    sha256               text,
    kind                 text,
    bytes                bigint,
    created_at           timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX published_artifacts_result_idx     ON published_artifacts (published_result_id);
CREATE INDEX published_artifacts_created_at_idx ON published_artifacts (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS published_artifacts;
DROP TABLE IF EXISTS published_results;
DROP TABLE IF EXISTS processing_environments;
