-- filename: 20261021120000001_rehearsal_captured_event_ids.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A captured event keeps its envelope ID so a retried publish of the same
-- source and ID is one event, as in production (ADR-20261021120000). Rows
-- captured before this column existed keep NULL and never deduplicate.
ALTER TABLE scenario_captured_events
 ADD COLUMN IF NOT EXISTS event_id text CHECK (event_id IS NULL OR char_length(event_id) BETWEEN 1 AND 256);

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE scenario_captured_events DROP COLUMN IF EXISTS event_id;
