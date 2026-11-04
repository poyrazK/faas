-- filename: 20261103093241660_issue_ticket_pending_index.sql
-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS native_issue_ticket_pending_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS native_issue_ticket_pending_idx
 ON native_issue_ticket_receipts (issue_id)
 WHERE state IN ('dispatching','created') OR (state='ready' AND claim_id IS NULL);
RESET lock_timeout;
-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS native_issue_ticket_pending_idx;
RESET lock_timeout;
