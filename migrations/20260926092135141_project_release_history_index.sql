-- +goose Up
CREATE INDEX IF NOT EXISTS project_release_sets_history_idx
    ON project_release_sets (project_id, environment_slug, created_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS project_release_sets_history_idx;
