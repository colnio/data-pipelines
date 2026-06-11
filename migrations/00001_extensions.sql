-- +goose Up
-- Extensions used across modules. gen_random_uuid() comes from pgcrypto;
-- pg_trgm powers fuzzy name/path search in the browse UI.
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- +goose Down
DROP EXTENSION IF EXISTS pg_trgm;
DROP EXTENSION IF EXISTS pgcrypto;
