-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- One immutable selection per retained source identity; no credentials are stored.
CREATE TABLE IF NOT EXISTS hosted_task_binding_qualifications (
 account_id uuid NOT NULL,
 id text NOT NULL CHECK (id ~ '^[0-9a-f]{64}$'),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object' AND octet_length(payload::text)<=32768),
 expires_at timestamptz NOT NULL,
 PRIMARY KEY(account_id,id),
 FOREIGN KEY(account_id,id) REFERENCES hosted_task_source_intents(account_id,id) ON DELETE CASCADE,
 CHECK ((payload->'Public'->>'account_id'=account_id::text
 AND payload->'Public'->>'intent_id'=id
 AND (payload->'Public'->>'expires_at')::timestamptz=expires_at
 AND payload->'Public'->>'status' IN ('selected','verifying','passed','failed','stale','revoked','expired')) IS TRUE),
 CHECK (payload->'Public' @> '{"ready_to_apply":false,"bindings_verified":false,"owner_authorization_required":true}'::jsonb)
);
-- +goose Down
DROP TABLE hosted_task_binding_qualifications;
