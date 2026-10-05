-- +goose Up
-- ADR-521: immutable completion retry decisions; parent retention bounds them.
CREATE TABLE customer_operation_delivery_retries (
 operation_id uuid NOT NULL REFERENCES customer_operations(id) ON DELETE CASCADE,
 retry_id text NOT NULL CHECK (octet_length(retry_id) BETWEEN 1 AND 128),
 delivery_id uuid NOT NULL,
 expected_replay_generation integer NOT NULL CHECK (expected_replay_generation >= 0 AND expected_replay_generation < 2147483647),
 replay_generation integer NOT NULL CHECK (replay_generation = expected_replay_generation + 1),
 queued_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK (expires_at > queued_at),
 PRIMARY KEY (operation_id, retry_id)
);

-- +goose Down
DROP TABLE customer_operation_delivery_retries;
