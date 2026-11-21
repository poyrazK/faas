-- Projection of the table neond reads and writes (ADR-20261112160000
-- decision 3): neon_project_keys, migration 20261112183015421.
CREATE TABLE neon_project_keys (
    key_id     bigint      PRIMARY KEY,
    project_id text        NOT NULL,
    sealed_key bytea       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);
