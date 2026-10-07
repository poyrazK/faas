-- +goose Up
-- ADR-531: each stage delivery attempt owns a fresh opaque receipt. Only its
-- digest is retained; receipts and messages are operational and reset at clone.
CREATE TABLE IF NOT EXISTS invocation_environment_queue_receipts (
    invocation_id uuid PRIMARY KEY REFERENCES invocations(id) ON DELETE CASCADE,
    attempt integer NOT NULL CHECK (attempt > 0),
    token_hash text NOT NULL CHECK (token_hash ~ '^[a-f0-9]{64}$'),
    owner_hash text NOT NULL CHECK (owner_hash ~ '^[a-f0-9]{64}$'),
    issued_at timestamptz NOT NULL,
    lease_expires_at timestamptz NOT NULL CHECK (lease_expires_at > issued_at)
);

CREATE OR REPLACE VIEW production_invocation_work AS
SELECT i.* FROM invocations i
WHERE i.environment_id IS NULL
    AND NOT EXISTS(SELECT 1 FROM invocation_environment_queue_admissions p WHERE p.invocation_id=i.id)
    AND NOT EXISTS(SELECT 1 FROM invocation_work_environment_admissions p WHERE p.invocation_id=i.id)
    AND NOT faas_invocation_headers_own_stage(i.app_id,i.headers)
    AND NOT EXISTS(SELECT 1 FROM invocation_environment_queue_receipts r WHERE r.invocation_id=i.id);

-- +goose Down
-- Retention must retire receipt-owned messages before removing their fence.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS(SELECT 1 FROM invocation_environment_queue_receipts) THEN
        RAISE EXCEPTION 'cannot remove stage queue receipts while owned messages remain';
    END IF;
END;
$$;
-- +goose StatementEnd
CREATE OR REPLACE VIEW production_invocation_work AS
SELECT i.* FROM invocations i
WHERE i.environment_id IS NULL
    AND NOT EXISTS(SELECT 1 FROM invocation_environment_queue_admissions p WHERE p.invocation_id=i.id)
    AND NOT EXISTS(SELECT 1 FROM invocation_work_environment_admissions p WHERE p.invocation_id=i.id)
    AND NOT faas_invocation_headers_own_stage(i.app_id,i.headers);
DROP TABLE invocation_environment_queue_receipts;
