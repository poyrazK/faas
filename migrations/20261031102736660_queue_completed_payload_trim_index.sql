-- migration-compat: expand
-- filename: 20261031102736660_queue_completed_payload_trim_index.sql

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
-- ADR-20261018101500 amendment 2026-10-31: a completed queue message keeps its
-- payload for a day after its ack, then only its history. The retention sweep
-- reads this index in completion order; a trimmed row leaves it.
CREATE INDEX CONCURRENTLY IF NOT EXISTS invocations_queue_completed_payload_idx
    ON invocations (completed_at)
    WHERE source = 'queue' AND state = 'completed' AND payload <> 'null'::jsonb;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS invocations_queue_completed_payload_idx;
-- +goose StatementEnd
