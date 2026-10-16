-- +goose Up
CREATE INDEX IF NOT EXISTS binding_verification_runs_listing_idx ON binding_verification_runs(account_id,app_id,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS project_binding_verification_runs_listing_idx ON project_binding_verification_runs(account_id,project_id,environment_slug,created_at DESC,id DESC);

-- +goose Down
DROP INDEX project_binding_verification_runs_listing_idx;
DROP INDEX binding_verification_runs_listing_idx;
