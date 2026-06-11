-- +goose Up
-- Condition labels and treatment events (architecture §8). Controlled vocabulary
-- for run conditions, plus a treatment-event table for interventions such as UV
-- exposure, annealing, and plasma treatment.

-- Controlled vocabulary of condition labels; aliases allow multiple spellings.
CREATE TABLE condition_labels (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    canonical_name text        UNIQUE NOT NULL,
    aliases_json   jsonb       NOT NULL DEFAULT '[]',
    description    text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- Many-to-many junction between runs and condition labels.
-- run_id is a bare text reference (no FK) consistent with how runs references
-- catalog tables; here we do add a FK because a run_condition_label row without
-- a real run is meaningless and CASCADE makes cleanup safe.
CREATE TABLE run_condition_labels (
    run_id             text    NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    condition_label_id bigint  NOT NULL REFERENCES condition_labels(id),
    PRIMARY KEY (run_id, condition_label_id)
);

CREATE INDEX run_condition_labels_run_idx   ON run_condition_labels (run_id);
CREATE INDEX run_condition_labels_label_idx ON run_condition_labels (condition_label_id);

-- Treatments are events on the device (UV, anneal, plasma, storage).
-- device_id is a bare text reference — catalog module owns that table.
CREATE TABLE treatment_events (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    device_id       text,
    treatment_type  text,
    started_at      timestamptz,
    ended_at        timestamptz,
    parameters_json jsonb       NOT NULL DEFAULT '{}',
    performed_by    text,
    notes           text,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX treatment_events_device_idx      ON treatment_events (device_id);
CREATE INDEX treatment_events_created_at_idx  ON treatment_events (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS treatment_events;
DROP TABLE IF EXISTS run_condition_labels;
DROP TABLE IF EXISTS condition_labels;
