-- filename: 20261016165524626_operator_alert_collection_history.sql

-- +goose Up
-- Prospective only: latest singleton timestamps are not historical receipts.
CREATE TABLE IF NOT EXISTS operator_alert_collection_outcomes (
    attempted_at timestamptz PRIMARY KEY CHECK (isfinite(attempted_at)),
    outcome text NOT NULL CHECK (outcome IN ('success', 'failure')),
    reason text NOT NULL CHECK (reason IN ('', 'collection_failed', 'source_read_failed', 'snapshot_rejected', 'timeout', 'persistence_failed')),
    CHECK ((outcome = 'success' AND reason = '') OR (outcome = 'failure' AND reason <> ''))
);

-- +goose Down
DROP TABLE IF EXISTS operator_alert_collection_outcomes;
