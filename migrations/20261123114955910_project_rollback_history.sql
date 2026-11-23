-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS project_rollback_history_by_project
    ON project_rollback_operations (project_id);

-- +goose Down
DROP INDEX CONCURRENTLY project_rollback_history_by_project;
