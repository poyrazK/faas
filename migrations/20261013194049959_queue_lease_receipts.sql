-- +goose Up
-- ADR-1140: each production pull lease owns a fresh opaque receipt. Only its
-- digest is kept; the attempt and replay generation fence a stale receipt
-- after the lease expires and the message is redelivered.
CREATE TABLE IF NOT EXISTS invocation_queue_lease_receipts (
    invocation_id uuid PRIMARY KEY REFERENCES invocations(id) ON DELETE CASCADE,
    attempt integer NOT NULL CHECK (attempt > 0),
    replay_generation bigint NOT NULL CHECK (replay_generation >= 0),
    token_hash text NOT NULL CHECK (token_hash ~ '^[a-f0-9]{64}$'),
    issued_at timestamptz NOT NULL,
    lease_expires_at timestamptz NOT NULL CHECK (lease_expires_at > issued_at)
);

-- +goose Down
DROP TABLE IF EXISTS invocation_queue_lease_receipts;
