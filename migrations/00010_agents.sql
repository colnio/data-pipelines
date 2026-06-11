-- +goose Up
-- Measurement-PC agents and their allowed filesystem roots (architecture §4).
-- The server trusts an authenticated agent but not infallibly: a manifest
-- meas_path is valid only if it resolves under one of the agent's roots.
--
-- agents.id is a human-meaningful agent identifier (e.g. "measpc-probestation-01"),
-- not a uuid. key_hash is a bcrypt hash of the agent's shared key — never store
-- the raw key.
CREATE TABLE agents (
    id            text        PRIMARY KEY,
    display_name  text        NOT NULL DEFAULT '',
    key_hash      text        NOT NULL,
    enabled       boolean     NOT NULL DEFAULT true,
    last_seen_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE agent_allowed_roots (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    agent_id  text   NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    root      text   NOT NULL,
    UNIQUE (agent_id, root)
);

CREATE INDEX agent_allowed_roots_agent_idx ON agent_allowed_roots (agent_id);

-- +goose Down
DROP TABLE IF EXISTS agent_allowed_roots;
DROP TABLE IF EXISTS agents;
