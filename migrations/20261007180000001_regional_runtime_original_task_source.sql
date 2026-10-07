-- ADR-653: retain the original durable task without a mutable-row foreign key.
-- +goose Up
ALTER TABLE regional_runtime_launch_sources ADD COLUMN task_id uuid;
ALTER TABLE regional_runtime_launch_sources ADD CONSTRAINT regional_launch_task_identity CHECK (
    task_id IS NULL OR (
        task_id <> '00000000-0000-0000-0000-000000000000'::uuid
        AND permit_claims->>'Kind' = 'app_task'
    )
);

-- +goose Down
-- Original task relationships are retained evidence and cannot be discarded.
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'regional runtime original task source rollback is unsupported';
END $$;
-- +goose StatementEnd
