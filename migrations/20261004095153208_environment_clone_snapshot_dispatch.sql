-- +goose Up
-- ADR-531: a lost request acknowledgement must not authorize a second POST.
ALTER TABLE project_environment_clone_postgres_snapshots
    DROP CONSTRAINT project_environment_clone_postgres_snapshots_state_check,
    ADD CHECK (state IN ('capturing','requested','retained','deleting','deleted')),
    ADD COLUMN request_started_at timestamptz,
    ADD CHECK (state NOT IN ('requested','retained') OR request_started_at IS NOT NULL);

-- +goose Down
ALTER TABLE project_environment_clone_postgres_snapshots
    DROP CONSTRAINT project_environment_clone_postgres_snapshots_state_check,
    DROP COLUMN request_started_at,
    ADD CHECK (state IN ('capturing','retained','deleting','deleted'));
