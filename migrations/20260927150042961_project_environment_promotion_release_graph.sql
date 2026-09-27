-- filename: 20260927150042961_project_environment_promotion_release_graph.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE project_environment_promotions
    ADD COLUMN IF NOT EXISTS release_graph_mode boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS source_release_set_id uuid REFERENCES project_release_sets(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS previous_target_release_set_id uuid REFERENCES project_release_sets(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS target_release_set_id uuid REFERENCES project_release_sets(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS restored_target_release_set_id uuid REFERENCES project_release_sets(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS release_ttl_seconds integer NOT NULL DEFAULT 0;

ALTER TABLE project_environment_promotion_workloads
    ADD COLUMN IF NOT EXISTS previous_target_traffic_percent integer NOT NULL DEFAULT 0;

DO $$
BEGIN
    ALTER TABLE project_environment_promotions
        ADD CONSTRAINT project_environment_promotions_release_ttl_check
        CHECK (release_ttl_seconds BETWEEN 0 AND 604800);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE project_environment_promotions
        ADD CONSTRAINT project_environment_promotions_release_graph_shape
        CHECK (NOT release_graph_mode OR release_ttl_seconds > 0);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE project_environment_promotion_workloads
        ADD CONSTRAINT project_environment_promotion_workloads_previous_traffic_check
        CHECK (previous_target_traffic_percent BETWEEN 0 AND 100);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE project_environment_promotions
    DROP CONSTRAINT IF EXISTS project_environment_promotions_release_graph_shape,
    DROP CONSTRAINT IF EXISTS project_environment_promotions_release_ttl_check,
    DROP COLUMN IF EXISTS release_ttl_seconds,
    DROP COLUMN IF EXISTS restored_target_release_set_id,
    DROP COLUMN IF EXISTS target_release_set_id,
    DROP COLUMN IF EXISTS previous_target_release_set_id,
    DROP COLUMN IF EXISTS source_release_set_id,
    DROP COLUMN IF EXISTS release_graph_mode;
ALTER TABLE project_environment_promotion_workloads
    DROP CONSTRAINT IF EXISTS project_environment_promotion_workloads_previous_traffic_check,
    DROP COLUMN IF EXISTS previous_target_traffic_percent;
-- +goose StatementEnd
