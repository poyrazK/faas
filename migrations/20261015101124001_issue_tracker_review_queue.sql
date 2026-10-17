-- +goose Up
CREATE INDEX IF NOT EXISTS issue_tracker_auto_sync_review_idx ON issue_tracker_auto_sync(app_id,link_id)
WHERE enabled AND (reason='fixing_deployment_required' OR outcome IN ('blocked','conflict','error') OR pending_state<>'');

-- +goose Down
DROP INDEX issue_tracker_auto_sync_review_idx;
