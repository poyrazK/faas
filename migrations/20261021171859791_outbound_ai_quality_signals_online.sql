-- filename: 20261021171859791_outbound_ai_quality_signals_online.sql
-- ADR 20261021171712: index the request log's pending online evaluations
-- for the evaluation worker, built concurrently so logging never pauses,
-- then validate the checks 20261021171858691, 20261021171858793 and
-- 20261021171858895 added NOT VALID. In a NO TRANSACTION migration every
-- statement is its own transaction; VALIDATE takes a SHARE UPDATE
-- EXCLUSIVE lock, so writes continue during each scan.

-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
CREATE INDEX CONCURRENTLY IF NOT EXISTS outbound_ai_request_log_evaluation_pending_idx
    ON outbound_ai_request_log (created_at) WHERE evaluation_state = 'pending';
ALTER TABLE outbound_ai_request_log VALIDATE CONSTRAINT outbound_ai_request_log_quality_chk;
ALTER TABLE outbound_ai_usage_daily VALIDATE CONSTRAINT outbound_ai_usage_daily_quality_chk;
ALTER TABLE outbound_integrations VALIDATE CONSTRAINT outbound_integrations_ai_quality_chk;
ALTER TABLE app_webhook_event_outbox VALIDATE CONSTRAINT app_webhook_event_outbox_event_chk;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS outbound_ai_request_log_evaluation_pending_idx;
