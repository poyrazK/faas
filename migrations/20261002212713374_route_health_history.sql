-- +goose Up
CREATE TABLE IF NOT EXISTS route_health_history (
    id uuid PRIMARY KEY,
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    decision_key text NOT NULL CHECK (decision_key ~ '^[0-9a-f]{64}$'),
    checked_at timestamptz NOT NULL CHECK (isfinite(checked_at)),
    encoded_bytes integer NOT NULL CHECK (encoded_bytes BETWEEN 1 AND 65536),
    entry jsonb NOT NULL CHECK (
        jsonb_typeof(entry) = 'object' AND entry->>'version' = '1'
        AND entry ?& ARRAY['id', 'report', 'decision', 'policy', 'checked_at', 'source']
        AND entry->>'id' = id::text
        AND entry->'report'->>'app_id' = app_id::text
        AND entry->'report'->>'deployment_id' = deployment_id::text
        AND octet_length(entry::text) <= 131072
    ),
    UNIQUE (deployment_id, decision_key)
);
CREATE INDEX IF NOT EXISTS route_health_history_deployment_idx ON route_health_history(deployment_id, checked_at DESC, id DESC);

-- +goose Down
-- Forward-only: retain saved rollout evidence.
SELECT 1;
