-- filename: 20261112183015421_neon_project_keys.sql
-- ADR-20261112160000 decision 3: neond mints a Neon API key scoped to each
-- project it creates, seals it to its own identity and keeps it here. Only
-- faas_neond (neond's login) may read or write a row: apid and every other
-- faas_runtime member get no access, so an apid bug or leak never reaches a
-- project key. The postgres role's runtime-roles.sql repeats the grants on
-- every converge, because faas_neond may not exist yet.
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS neon_project_keys (
    key_id     bigint      PRIMARY KEY CHECK (key_id > 0),
    project_id text        NOT NULL CHECK (project_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$'),
    sealed_key bytea       NOT NULL CHECK (octet_length(sealed_key) BETWEEN 1 AND 8192),
    created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at)),
    revoked_at timestamptz CHECK (revoked_at IS NULL OR (isfinite(revoked_at) AND revoked_at >= created_at))
);
-- One live key per project; revoked keys stay as the record of what was issued.
CREATE UNIQUE INDEX IF NOT EXISTS neon_project_keys_live ON neon_project_keys (project_id) WHERE revoked_at IS NULL;

DO $$
BEGIN
    IF to_regrole('faas_runtime') IS NOT NULL THEN
        REVOKE ALL ON neon_project_keys FROM faas_runtime;
    END IF;
    IF to_regrole('faas_neond') IS NOT NULL THEN
        GRANT SELECT, INSERT, UPDATE ON neon_project_keys TO faas_neond;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS neon_project_keys;
-- +goose StatementEnd
