-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE INDEX IF NOT EXISTS project_submissions_safe_release_pending_idx ON project_submissions (updated_at)
WHERE payload #>> '{apply,safe_release,status}' IN ('building','verifying','rolling_back')
 OR payload #>> '{apply,safe_release,verification,stabilization,status}' = 'monitoring';

-- +goose Down
DROP INDEX project_submissions_safe_release_pending_idx;
