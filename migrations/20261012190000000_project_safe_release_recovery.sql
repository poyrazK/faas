-- +goose Up
CREATE INDEX project_submissions_safe_release_pending_idx ON project_submissions (updated_at)
WHERE payload #>> '{apply,safe_release,status}' = 'building';

-- +goose Down
DROP INDEX project_submissions_safe_release_pending_idx;
