-- readonly_role.sql — the labdata_readonly role + a labdata_nb login user for
-- JupyterHub notebooks (architecture §18 "read-only role by default", §22).
--
-- Idempotent and re-runnable (after adding views, re-run to grant on them).
-- Kept OUT of goose migrations on purpose: roles are cluster-global and the
-- login password is environment-specific, neither belongs in schema migrations
-- that run on every boot.
--
-- Run AFTER migrations, substituting the password placeholder, e.g.:
--   sed 's/__NB_PASSWORD__/your-secret/' readonly_role.sql | psql "$DATABASE_URL"
-- The dev compose init container and the Go enforcement test do this.
--
-- NOTE: PUBLIC has CONNECT on databases by default, so labdata_nb can connect.
-- If your cluster revokes that, also: GRANT CONNECT ON DATABASE <db> TO labdata_readonly;

DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'labdata_readonly') THEN
        CREATE ROLE labdata_readonly NOLOGIN;
    END IF;
END $$;

GRANT USAGE ON SCHEMA public TO labdata_readonly;

-- SELECT on the catalog views ONLY. labdata_readonly has no privilege on any
-- base table, so notebooks can read through the views but cannot touch raw
-- production tables.
GRANT SELECT ON
    v_samples, v_devices, v_contact_configs, v_runs, v_run_files,
    v_run_state_transitions, v_condition_labels, v_run_condition_labels,
    v_treatment_events, v_sample_parameter_versions, v_contact_geometry_versions,
    v_processing_parameter_versions, v_calibration_versions, v_parser_results,
    v_analysis_artifacts, v_review_artifacts, v_processing_environments,
    v_published_results, v_published_artifacts
TO labdata_readonly;

-- Login user that inherits the read-only role. Notebooks connect as this user.
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'labdata_nb') THEN
        CREATE ROLE labdata_nb LOGIN PASSWORD '__NB_PASSWORD__';
    ELSE
        ALTER ROLE labdata_nb WITH LOGIN PASSWORD '__NB_PASSWORD__';
    END IF;
END $$;

GRANT labdata_readonly TO labdata_nb;
