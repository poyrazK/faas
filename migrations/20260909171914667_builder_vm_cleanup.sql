-- +goose Up
-- A build row can become terminal before builderd successfully asks vmmd to
-- stop its VM (for example when a cancellation notification races a daemon
-- restart). Keep that teardown obligation durable so another builderd pass can
-- retry it without depending on a second notification.
CREATE TABLE IF NOT EXISTS builder_vm_cleanup (
    build_id       uuid PRIMARY KEY REFERENCES builds(id) ON DELETE CASCADE,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    claimed_at     timestamptz,
    claim_token    uuid,
    attempts       integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error     text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS builder_vm_cleanup_due_idx
    ON builder_vm_cleanup (next_attempt_at, build_id)
    WHERE claimed_at IS NULL;

CREATE INDEX IF NOT EXISTS builder_vm_cleanup_claimed_idx
    ON builder_vm_cleanup (claimed_at)
    WHERE claimed_at IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS builder_vm_cleanup;
