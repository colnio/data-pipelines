-- +goose Up
-- Catalog entities: samples, devices, and contact configurations (architecture §7).
-- These tables carry structural identity. Runs reference them by bare text id with
-- NO FK (intentional: a run can be ingested before catalog registration is complete,
-- and the catalog can be corrected independently without touching runs).

-- A sample represents the physical/material stack identity, e.g. '9D66P1'.
CREATE TABLE samples (
    id                 text        PRIMARY KEY,
    material_stack     text,
    dielectric         jsonb,
    fabrication_batch  text,
    params_json        jsonb,
    notes              text,
    created_at         timestamptz NOT NULL DEFAULT now()
);

-- A device is the physical measured part, e.g. 'gr_mob_31_f1'.
CREATE TABLE devices (
    id               text        PRIMARY KEY,
    sample_id        text        REFERENCES samples(id),
    device_class     text        NOT NULL,
    fabrication_id   text,
    lifecycle_state  text,
    notes            text,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX devices_sample_idx ON devices (sample_id);

-- A contact configuration defines the terminal pairing and active geometry.
-- L/W belong on contact_config, not on device or run (architecture §7).
CREATE TABLE contact_configs (
    id                   text        PRIMARY KEY,
    device_id            text        REFERENCES devices(id),
    terminal_roles_json  jsonb,
    is_default           boolean     NOT NULL DEFAULT false,
    notes                text,
    created_at           timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX contact_configs_device_idx ON contact_configs (device_id);

-- +goose Down
DROP TABLE IF EXISTS contact_configs;
DROP TABLE IF EXISTS devices;
DROP TABLE IF EXISTS samples;
