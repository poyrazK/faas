-- filename: 20261103093241400_issue_request_audit_lookup.sql
-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
-- A failed concurrent build can leave an invalid index under this name.
DROP INDEX CONCURRENTLY IF EXISTS request_audit_events_issue_request_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS request_audit_events_issue_request_idx
 ON request_audit_events (account_id, app_id, deployment_id, request_id, occurred_at);
RESET lock_timeout;
-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS request_audit_events_issue_request_idx;
RESET lock_timeout;
