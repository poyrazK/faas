-- filename: 20261103093241590_issue_unknown_http_weight_index.sql
-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
-- A failed concurrent build can leave an invalid index under this name.
DROP INDEX CONCURRENTLY IF EXISTS issue_events_unknown_http_weight_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS issue_events_unknown_http_weight_idx
 ON issue_events (issue_id, occurred_at)
 WHERE NOT occurrence_weight_known AND occurrence_count = 1 AND payload->>'source_kind' = 'http';
RESET lock_timeout;
-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS issue_events_unknown_http_weight_idx;
RESET lock_timeout;
