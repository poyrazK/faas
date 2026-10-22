-- +goose Up
ALTER TABLE event_execution_coverage ADD COLUMN IF NOT EXISTS lineage_repair_progress jsonb NOT NULL DEFAULT '{}'::jsonb;
-- +goose Down
ALTER TABLE event_execution_coverage DROP COLUMN IF EXISTS lineage_repair_progress;
