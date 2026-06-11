-- +goose Up
-- Parameter versioning (architecture §9). Every scientific parameter that can
-- change over time gets its own version table. Review-time corrections create
-- new versions; previous versions are never overwritten. The manifest pins
-- parameter versions known at measurement declaration time.

-- Sample-level parameter versions: oxide thickness, dielectric, stack composition.
CREATE TABLE sample_parameter_versions (
    id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sample_id             text        NOT NULL,
    version               int         NOT NULL,
    oxide_thickness_nm    double precision,
    dielectric            text,
    stack_composition_json jsonb,
    params_json           jsonb,
    created_at            timestamptz NOT NULL DEFAULT now(),
    UNIQUE (sample_id, version)
);

CREATE INDEX sample_parameter_versions_sample_idx ON sample_parameter_versions (sample_id);

-- Contact geometry versions: L, W, terminal roles.
CREATE TABLE contact_geometry_versions (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    contact_config_id    text        NOT NULL,
    version              int         NOT NULL,
    length_um            double precision,
    width_um             double precision,
    terminal_roles_json  jsonb,
    created_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (contact_config_id, version)
);

CREATE INDEX contact_geometry_versions_config_idx ON contact_geometry_versions (contact_config_id);

-- Processing parameter versions: fit windows, threshold definitions, smoothing choices.
-- scope + scope_key identify what this version applies to, e.g. scope='parser', scope_key='fet_transfer'.
CREATE TABLE processing_parameter_versions (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scope       text        NOT NULL,
    scope_key   text        NOT NULL,
    version     int         NOT NULL,
    params_json jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (scope, scope_key, version)
);

CREATE INDEX processing_parameter_versions_scope_idx ON processing_parameter_versions (scope, scope_key);

-- Calibration versions: instrument calibration constants.
CREATE TABLE calibration_versions (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    instrument     text        NOT NULL,
    version        int         NOT NULL,
    constants_json jsonb,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (instrument, version)
);

CREATE INDEX calibration_versions_instrument_idx ON calibration_versions (instrument);

-- +goose Down
DROP TABLE IF EXISTS calibration_versions;
DROP TABLE IF EXISTS processing_parameter_versions;
DROP TABLE IF EXISTS contact_geometry_versions;
DROP TABLE IF EXISTS sample_parameter_versions;
