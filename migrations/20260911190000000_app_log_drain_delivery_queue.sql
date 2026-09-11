-- filename: 20260911190000000_app_log_drain_delivery_queue.sql
-- +goose Up
-- +goose StatementBegin

-- Durable gateway-local outboxes report their backlog through the existing
-- customer-safe health snapshot. The byte capacity is separate from the
-- legacy in-memory queue fields so the dashboard does not mix units.
ALTER TABLE app_log_drain_health
    ADD COLUMN IF NOT EXISTS pending_records integer NOT NULL DEFAULT 0
        CHECK (pending_records >= 0),
    ADD COLUMN IF NOT EXISTS pending_bytes bigint NOT NULL DEFAULT 0
        CHECK (pending_bytes >= 0),
    ADD COLUMN IF NOT EXISTS pending_bytes_capacity bigint NOT NULL DEFAULT 0
        CHECK (pending_bytes_capacity >= 0),
    ADD COLUMN IF NOT EXISTS dead_letter_total bigint NOT NULL DEFAULT 0
        CHECK (dead_letter_total >= 0),
    ADD COLUMN IF NOT EXISTS oldest_pending_at timestamptz;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_log_drain_health
    DROP COLUMN IF EXISTS oldest_pending_at,
    DROP COLUMN IF EXISTS dead_letter_total,
    DROP COLUMN IF EXISTS pending_bytes_capacity,
    DROP COLUMN IF EXISTS pending_bytes,
    DROP COLUMN IF EXISTS pending_records;
-- +goose StatementEnd
