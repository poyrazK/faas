-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- A rehearsal plans one disposable allocation per qualified source identity.
CREATE TABLE IF NOT EXISTS hosted_task_rehearsal_plans (
    account_id uuid NOT NULL,
    id text NOT NULL CHECK (id ~ '^[0-9a-f]{64}$'),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 4096),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, id),
    FOREIGN KEY (account_id, id) REFERENCES hosted_task_binding_qualifications(account_id, id) ON DELETE CASCADE,
    CHECK ((payload->'Plan'->>'account_id' = account_id::text
        AND payload->'Plan'->>'intent_id' = id
        AND (payload->'Plan'->>'expires_at')::timestamptz = expires_at
        AND payload->'Plan'->>'status' IN ('planned', 'approved', 'claimed', 'revoked', 'stale', 'expired')
        AND payload->'Plan' @> '{"no_traffic":true}'::jsonb) IS TRUE)
);

-- +goose Down
DROP TABLE IF EXISTS hosted_task_rehearsal_plans;
