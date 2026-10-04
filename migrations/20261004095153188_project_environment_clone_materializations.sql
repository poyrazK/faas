-- +goose Up
-- ADR-531: retain the original target lifetime after a materialization crash.
-- No environment FK: deletion must not erase the identity and permit adoption
-- of a new environment which happens to have the same slug.
CREATE TABLE project_environment_clone_materializations (
    operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_operations(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL UNIQUE CHECK (environment_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    workload_settings jsonb NOT NULL CHECK (jsonb_typeof(workload_settings)='object' AND workload_settings<>'{}'::jsonb),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
DROP TABLE project_environment_clone_materializations;
