-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR-20261112160000: an invocation a function sent with an SDK functions
-- client's send(). sender_app_id is the calling app, from node-local instance
-- identity; only it may read the invocation from its VM. on_complete_app_id,
-- when set, receives a completion invocation when this one is terminal.
-- Written in the same transaction as the invocation row.
CREATE TABLE IF NOT EXISTS invocation_sends (
 invocation_id uuid PRIMARY KEY REFERENCES invocations(id) ON DELETE CASCADE,
 sender_app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 on_complete_app_id uuid REFERENCES apps(id) ON DELETE SET NULL,
 created_at timestamptz NOT NULL DEFAULT now() CHECK(isfinite(created_at))
);
CREATE INDEX IF NOT EXISTS invocation_sends_sender_idx ON invocation_sends(sender_app_id, created_at);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS invocation_sends;
