-- filename: 20261107150000000_environment_branch_capture_objects.sql
-- ADR-20261031193635 slice 5 follow-up: every object a credential-free capture
-- attempt writes. schedd records the key before the snapshot is taken; imaged
-- deletes the objects of a key no ready capture references (a failed or retried
-- attempt, a retired capture, a deleted parent deployment) and then the row. No
-- foreign key: the row must outlive its capture and its deployment. A new table,
-- so older daemons keep working; an older schedd records nothing, and its
-- attempts leave objects behind as before.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS environment_branch_capture_objects (
    storage_key   text        PRIMARY KEY,
    deployment_id uuid        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT environment_branch_capture_objects_key CHECK (
        octet_length(storage_key) <= 512 AND storage_key LIKE 'snap/%/v2/mem')
);
CREATE INDEX IF NOT EXISTS environment_branch_capture_objects_created_idx
    ON environment_branch_capture_objects (created_at);
COMMENT ON TABLE environment_branch_capture_objects IS
    'ADR-20261031193635 objects written by credential-free capture attempts; imaged deletes those no ready capture references.';
-- Captures published before this table existed.
INSERT INTO environment_branch_capture_objects (storage_key, deployment_id, created_at)
SELECT storage_key, deployment_id, requested_at
  FROM environment_branch_captures
 WHERE storage_key LIKE 'snap/%/v2/mem' AND octet_length(storage_key) <= 512
ON CONFLICT (storage_key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS environment_branch_capture_objects;
