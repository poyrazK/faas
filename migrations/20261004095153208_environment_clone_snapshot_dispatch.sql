-- +goose Up
-- ADR-531: a lost request acknowledgement must not authorize a second POST.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='project_environment_clone_postgres_snapshots'::regclass AND attname='request_started_at' AND NOT attisdropped) THEN
        ALTER TABLE project_environment_clone_postgres_snapshots
            DROP CONSTRAINT project_environment_clone_postgres_snapshots_state_check,
            ADD CHECK (state IN ('capturing','requested','retained','deleting','deleted')),
            ADD COLUMN IF NOT EXISTS request_started_at timestamptz,
            ADD CHECK (state NOT IN ('requested','retained') OR request_started_at IS NOT NULL);
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE project_environment_clone_postgres_snapshots
    DROP CONSTRAINT project_environment_clone_postgres_snapshots_state_check,
    DROP COLUMN request_started_at,
    ADD CHECK (state IN ('capturing','retained','deleting','deleted'));
