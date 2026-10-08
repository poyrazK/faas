-- filename: 20261007150000001_customer_operation_milestones.sql
-- ADR-715: retained public business facts and cross-generation deduplication.
-- Replay-safety: guard the named CHECK, create the table/index if absent, and
-- drop/re-add the widened event-type CHECK.
-- +goose Up
CREATE TABLE IF NOT EXISTS customer_operation_milestones (
    operation_id uuid NOT NULL REFERENCES customer_operations(id) ON DELETE CASCADE,
    id uuid NOT NULL,
    event_sequence bigint NOT NULL CHECK (event_sequence > 0),
    name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,63}$'),
    payload json NOT NULL CHECK (octet_length(payload::text) BETWEEN 1 AND 8192),
    occurred_at timestamptz NOT NULL CHECK (isfinite(occurred_at)),
    created_at timestamptz NOT NULL CHECK (isfinite(created_at)),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (operation_id, id),
    UNIQUE (operation_id, event_sequence)
);
CREATE INDEX IF NOT EXISTS customer_operation_milestones_history_idx ON customer_operation_milestones
    (operation_id, created_at DESC, id DESC);
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'customer_operation_milestone_count_valid'
          AND conrelid = 'customer_operations'::regclass
    ) THEN
        ALTER TABLE customer_operations ADD CONSTRAINT customer_operation_milestone_count_valid CHECK (
            NOT (record ? 'milestone_count') OR coalesce(
                jsonb_typeof(record->'milestone_count') = 'number'
                AND (record->>'milestone_count')::integer BETWEEN 0 AND 64, false)
        );
    END IF;
END$$;
-- +goose StatementEnd
ALTER TABLE customer_operation_events DROP CONSTRAINT IF EXISTS customer_operation_events_event_type_check;
ALTER TABLE customer_operation_events ADD CONSTRAINT customer_operation_events_event_type_check
    CHECK (event_type IN ('accepted','running','progress','workflow_progress','artifact_attached','milestone','succeeded','failed','cancellation_requested','cancelled','reconciliation_required','recovery_requested','delivery_changed','result_expired'));

-- +goose Down
-- Retained business facts make rollback unsafe until their Operations expire.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM customer_operation_milestones) THEN
        RAISE EXCEPTION 'retained customer milestones prevent rollback';
    END IF;
END $$;
-- +goose StatementEnd
DELETE FROM customer_operation_events WHERE event_type='milestone';
ALTER TABLE customer_operation_events DROP CONSTRAINT IF EXISTS customer_operation_events_event_type_check;
ALTER TABLE customer_operation_events ADD CONSTRAINT customer_operation_events_event_type_check
    CHECK (event_type IN ('accepted','running','progress','workflow_progress','artifact_attached','succeeded','failed','cancellation_requested','cancelled','reconciliation_required','recovery_requested','delivery_changed','result_expired'));
ALTER TABLE customer_operations DROP CONSTRAINT IF EXISTS customer_operation_milestone_count_valid;
DROP TABLE IF EXISTS customer_operation_milestones;
