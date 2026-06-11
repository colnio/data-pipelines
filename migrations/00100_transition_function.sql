-- +goose Up
-- Cross-language run-state transition contract (architecture §16). Python
-- pipeline workers cannot call the Go statemachine package, so legality lives
-- in an allowed_transitions table and a transition_run() function that both
-- languages call. The seeded edges MUST match internal/statemachine/transitions.go
-- (a Go parity test guards against drift).

CREATE TABLE allowed_transitions (
    from_state text NOT NULL,
    to_state   text NOT NULL,
    PRIMARY KEY (from_state, to_state)
);

INSERT INTO allowed_transitions (from_state, to_state) VALUES
    ('declared','pulling'), ('declared','transfer_failed'),
    ('pulling','unpacked'), ('pulling','transfer_failed'), ('pulling','unsafe_archive'),
    ('unpacked','verified'), ('unpacked','unsafe_archive'), ('unpacked','manifest_mismatch'), ('unpacked','pulling'),
    ('verified','promoted'), ('verified','manifest_mismatch'), ('verified','pulling'),
    ('promoted','needs_metadata'), ('promoted','parsing'),
    ('needs_metadata','parsing'),
    ('parsing','validated'), ('parsing','parser_failed'), ('parsing','quarantined'),
    ('validated','processing'),
    ('processing','awaiting_review'), ('processing','processing_failed'),
    ('awaiting_review','approved'), ('awaiting_review','changes_requested'),
    ('awaiting_review','quarantined'), ('awaiting_review','review_rejected'),
    ('changes_requested','processing'), ('changes_requested','awaiting_review'),
    ('approved','publishing'),
    ('publishing','published'), ('publishing','processing_failed'),
    ('transfer_failed','pulling'),
    ('manifest_mismatch','pulling'),
    ('parser_failed','parsing'),
    ('processing_failed','processing'),
    ('quarantined','parsing'), ('quarantined','processing');

-- +goose StatementBegin
-- transition_run validates and applies a run state change, writing the audit
-- row, all under a row lock. Raises:
--   'run_not_found'      when the run does not exist
--   'state_mismatch'     when p_expected_from is non-empty and != current state
--   'illegal_transition' when (current -> p_to) is not in allowed_transitions
-- Returns the id of the inserted run_state_transitions row.
CREATE OR REPLACE FUNCTION transition_run(
    p_run_id        text,
    p_expected_from text,
    p_to            text,
    p_actor_type    text,
    p_actor_id      text,
    p_reason        text,
    p_payload       jsonb DEFAULT '{}'::jsonb
) RETURNS bigint
LANGUAGE plpgsql
AS $$
DECLARE
    v_current   text;
    v_trans_id  bigint;
BEGIN
    SELECT state INTO v_current FROM runs WHERE id = p_run_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'run_not_found: %', p_run_id USING ERRCODE = 'P0002';
    END IF;

    IF p_expected_from IS NOT NULL AND p_expected_from <> '' AND v_current <> p_expected_from THEN
        RAISE EXCEPTION 'state_mismatch: expected % but run is %', p_expected_from, v_current
            USING ERRCODE = 'P0001';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM allowed_transitions WHERE from_state = v_current AND to_state = p_to
    ) THEN
        RAISE EXCEPTION 'illegal_transition: % -> %', v_current, p_to USING ERRCODE = 'P0001';
    END IF;

    UPDATE runs SET state = p_to, updated_at = now() WHERE id = p_run_id;

    INSERT INTO run_state_transitions (run_id, from_state, to_state, actor_type, actor_id, reason, payload_json)
    VALUES (p_run_id, v_current, p_to, p_actor_type, p_actor_id, COALESCE(p_reason, ''), COALESCE(p_payload, '{}'::jsonb))
    RETURNING id INTO v_trans_id;

    RETURN v_trans_id;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS transition_run(text, text, text, text, text, text, jsonb);
DROP TABLE IF EXISTS allowed_transitions;
