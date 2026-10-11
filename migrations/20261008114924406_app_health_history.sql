-- filename: 20261008114924406_app_health_history.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS app_health_collection_state (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    next_check_at timestamptz NOT NULL CHECK (isfinite(next_check_at)),
    lease_token text,
    lease_started_at timestamptz,
    lease_until timestamptz,
    checked_at timestamptz,
    assessment_key text CHECK (assessment_key ~ '^[0-9a-f]{64}$'),
    assessment jsonb CHECK (jsonb_typeof(assessment) = 'object' AND assessment->>'app_id' = app_id::text AND assessment->>'scope' = 'default' AND octet_length(assessment::text) <= 131072),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL) AND (lease_token IS NULL) = (lease_started_at IS NULL)),
    CHECK (lease_token IS NULL OR (length(lease_token) > 0 AND isfinite(lease_started_at) AND isfinite(lease_until) AND lease_until > lease_started_at)),
    CHECK ((checked_at IS NULL) = (assessment IS NULL) AND (assessment_key IS NULL) = (assessment IS NULL)),
    CHECK (checked_at IS NULL OR isfinite(checked_at))
);
CREATE INDEX IF NOT EXISTS app_health_collection_due_idx ON app_health_collection_state(next_check_at, app_id);

CREATE TABLE IF NOT EXISTS app_health_history (
    id uuid PRIMARY KEY,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    observed_at timestamptz NOT NULL CHECK (isfinite(observed_at)),
    kind text NOT NULL CHECK (kind IN ('baseline', 'transition', 'gap')),
    encoded_bytes integer NOT NULL CHECK (encoded_bytes BETWEEN 1 AND 65536),
    entry jsonb NOT NULL CHECK (jsonb_typeof(entry) = 'object' AND entry->>'id' = id::text AND entry->>'kind' = kind AND entry->'assessment'->>'app_id' = app_id::text AND entry->'assessment'->>'scope' = 'default' AND octet_length(entry::text) <= 131072)
);
CREATE INDEX IF NOT EXISTS app_health_history_app_idx ON app_health_history(app_id, observed_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS app_health_history_retention_idx ON app_health_history(observed_at, id);

-- +goose Down
-- Forward-only: retain collected evidence.
SELECT 1;
