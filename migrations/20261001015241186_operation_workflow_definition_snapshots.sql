-- filename: 20261001015241186_operation_workflow_definition_snapshots.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE customer_operation_definitions ADD COLUMN IF NOT EXISTS workflow_snapshot jsonb;
DO $migration$
BEGIN
IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'customer_operation_definition_target'
    AND conrelid = 'customer_operation_definitions'::regclass) THEN
ALTER TABLE customer_operation_definitions ADD CONSTRAINT customer_operation_definition_target CHECK ((
    (NOT (spec ? 'workflow') AND workflow_snapshot IS NULL)
    OR (
        jsonb_typeof(spec->'workflow') = 'string'
        AND spec->>'workflow' <> ''
        AND jsonb_typeof(workflow_snapshot) = 'object'
        AND workflow_snapshot->>'name' = spec->>'workflow'
        AND jsonb_typeof(workflow_snapshot->'steps') = 'array'
    )
) IS TRUE);
END IF;
END;
$migration$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE customer_operation_definitions DROP CONSTRAINT IF EXISTS customer_operation_definition_target;
ALTER TABLE customer_operation_definitions DROP COLUMN IF EXISTS workflow_snapshot;
-- +goose StatementEnd
