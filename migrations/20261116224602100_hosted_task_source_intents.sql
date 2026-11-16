-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261116183545: ciphertext is short-lived; identity tombstones are immutable.
CREATE TABLE IF NOT EXISTS hosted_task_source_intents (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 id text NOT NULL CHECK (id ~ '^[0-9a-f]{64}$'),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object' AND octet_length(payload::text)<=131072),
 ciphertext bytea CHECK (ciphertext IS NULL OR octet_length(ciphertext) BETWEEN 1 AND 8388608),
 expires_at timestamptz NOT NULL,
 PRIMARY KEY(account_id,id),
 CHECK ((payload->>'id'=id AND payload->>'account_id'=account_id::text AND payload->>'source_state'='retained') IS TRUE),
 CHECK (payload @> '{"ready_to_apply":false,"bindings_verified":false,"owner_authorization_required":true}'::jsonb)
);
CREATE INDEX IF NOT EXISTS hosted_task_source_expiry_idx ON hosted_task_source_intents(expires_at) WHERE ciphertext IS NOT NULL;
-- +goose Down
DROP TABLE hosted_task_source_intents;
