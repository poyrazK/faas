-- +goose Up
DROP INDEX IF EXISTS project_submissions_safe_release_pending_idx;
CREATE INDEX IF NOT EXISTS project_submissions_safe_release_pending_idx ON project_submissions(updated_at)
WHERE payload #>> '{apply,safe_release,status}' IN ('building','verifying','rolling_back');

-- +goose Down
DROP INDEX project_submissions_safe_release_pending_idx;
CREATE INDEX project_submissions_safe_release_pending_idx ON project_submissions(updated_at)
WHERE payload #>> '{apply,safe_release,status}' = 'building';
