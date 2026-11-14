-- migration-compat: expand
-- Retain the start of an uninterrupted graph-routing interval across republish.
-- NULL on older writers/heads means unknown history; rollback checks fail closed.
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE project_release_sets ADD COLUMN IF NOT EXISTS fallback_hidden_at timestamptz;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE project_release_sets DROP COLUMN fallback_hidden_at;
