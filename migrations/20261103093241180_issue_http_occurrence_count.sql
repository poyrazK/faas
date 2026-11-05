-- filename: 20261103093241180_issue_http_occurrence_count.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE issue_events ADD COLUMN IF NOT EXISTS occurrence_count bigint NOT NULL DEFAULT 1;
-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM issue_events WHERE occurrence_count <> 1) THEN
  RAISE EXCEPTION 'weighted HTTP issue counts are retained; dropping occurrence_count would lose evidence';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE issue_events DROP COLUMN IF EXISTS occurrence_count;
