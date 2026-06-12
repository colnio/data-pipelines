-- +goose Up
-- Read-only catalog views for JupyterHub notebooks and the browse UI
-- (architecture §18 read-only access, §23 "use views for notebook/browse access
-- rather than exposing every internal table directly").
--
-- These views are the ONLY surface the labdata_readonly role is granted on
-- (see deploy/jupyterhub/readonly_role.sql). There are intentionally NO views
-- over users, agents, jobs, manifests.raw_json secrets, or idempotency/rate-limit
-- tables. Views default to security-definer, so a reader granted SELECT on the
-- view reads through the owner's privileges without any access to base tables.

CREATE VIEW v_samples AS
    SELECT id, material_stack, dielectric, fabrication_batch, params_json, notes, created_at
    FROM samples;

CREATE VIEW v_devices AS
    SELECT id, sample_id, device_class, fabrication_id, lifecycle_state, notes, created_at
    FROM devices;

CREATE VIEW v_contact_configs AS
    SELECT id, device_id, terminal_roles_json, is_default, notes, created_at
    FROM contact_configs;

CREATE VIEW v_runs AS
    SELECT id, manifest_hash, agent_id, measurement_type, completion_source,
           sample_id, device_id, contact_config_id, meas_path, operator_comment,
           state, declared_by, declared_at, created_at, updated_at
    FROM runs;

CREATE VIEW v_run_files AS
    SELECT id, run_id, name, bytes, sha256, created_at
    FROM run_files;

CREATE VIEW v_run_state_transitions AS
    SELECT id, run_id, from_state, to_state, actor_type, actor_id, reason, payload_json, created_at
    FROM run_state_transitions;

CREATE VIEW v_condition_labels AS
    SELECT id, canonical_name, aliases_json, description, created_at
    FROM condition_labels;

CREATE VIEW v_run_condition_labels AS
    SELECT run_id, condition_label_id
    FROM run_condition_labels;

CREATE VIEW v_treatment_events AS
    SELECT id, device_id, treatment_type, started_at, ended_at, parameters_json, performed_by, notes, created_at
    FROM treatment_events;

CREATE VIEW v_sample_parameter_versions AS
    SELECT id, sample_id, version, oxide_thickness_nm, dielectric, stack_composition_json, params_json, created_at
    FROM sample_parameter_versions;

CREATE VIEW v_contact_geometry_versions AS
    SELECT id, contact_config_id, version, length_um, width_um, terminal_roles_json, created_at
    FROM contact_geometry_versions;

CREATE VIEW v_processing_parameter_versions AS
    SELECT id, scope, scope_key, version, params_json, created_at
    FROM processing_parameter_versions;

CREATE VIEW v_calibration_versions AS
    SELECT id, instrument, version, constants_json, created_at
    FROM calibration_versions;

CREATE VIEW v_parser_results AS
    SELECT id, run_id, parser_version, status, columns_expected, rows_declared, rows_actual,
           warnings_json, output_json, created_at
    FROM parser_results;

CREATE VIEW v_analysis_artifacts AS
    SELECT id, run_id, analysis_version, kind, path, sha256, meta_json, created_at
    FROM analysis_artifacts;

CREATE VIEW v_review_artifacts AS
    SELECT id, run_id, version, summary_json, plots_json, metrics_json, parser_warnings_json, llm_summary, created_at
    FROM review_artifacts;

CREATE VIEW v_processing_environments AS
    SELECT id, created_at, python_version, os_image_or_container_digest, lock_hash,
           critical_packages_json, docker_image_digest, entrypoint_command, notes
    FROM processing_environments;

CREATE VIEW v_published_results AS
    SELECT id, run_id, manifest_hash, raw_file_hashes_json, parameter_version_ids_json,
           processing_git_sha, processing_dirty_tree, processing_environment_id, processing_command,
           parser_version, instrument_software_version, random_seed, review_decision_id,
           superseded_by, published_by, published_at
    FROM published_results;

CREATE VIEW v_published_artifacts AS
    SELECT id, published_result_id, path, sha256, kind, bytes, created_at
    FROM published_artifacts;

-- +goose Down
DROP VIEW IF EXISTS v_published_artifacts;
DROP VIEW IF EXISTS v_published_results;
DROP VIEW IF EXISTS v_processing_environments;
DROP VIEW IF EXISTS v_review_artifacts;
DROP VIEW IF EXISTS v_analysis_artifacts;
DROP VIEW IF EXISTS v_parser_results;
DROP VIEW IF EXISTS v_calibration_versions;
DROP VIEW IF EXISTS v_processing_parameter_versions;
DROP VIEW IF EXISTS v_contact_geometry_versions;
DROP VIEW IF EXISTS v_sample_parameter_versions;
DROP VIEW IF EXISTS v_treatment_events;
DROP VIEW IF EXISTS v_run_condition_labels;
DROP VIEW IF EXISTS v_condition_labels;
DROP VIEW IF EXISTS v_run_state_transitions;
DROP VIEW IF EXISTS v_run_files;
DROP VIEW IF EXISTS v_runs;
DROP VIEW IF EXISTS v_contact_configs;
DROP VIEW IF EXISTS v_devices;
DROP VIEW IF EXISTS v_samples;
