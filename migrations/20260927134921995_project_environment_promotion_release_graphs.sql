-- filename: 20260927134921995_project_environment_promotion_release_graphs.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE project_environment_promotions
    ADD COLUMN source_release_set_id text NOT NULL DEFAULT '',
    ADD COLUMN previous_target_release_set_id text NOT NULL DEFAULT '',
    ADD COLUMN target_release_set_id text NOT NULL DEFAULT '',
    ADD COLUMN rollback_release_set_id text NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE project_environment_promotions
    DROP COLUMN rollback_release_set_id,
    DROP COLUMN target_release_set_id,
    DROP COLUMN previous_target_release_set_id,
    DROP COLUMN source_release_set_id;
-- +goose StatementEnd
