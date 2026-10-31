-- filename: 20261020202333289_outbound_ai_traces_online.sql
-- ADR 20261020202035: index the request log by trace for a trace's call
-- list, built concurrently so logging never pauses, then validate the
-- checks 20261020202332229 added NOT VALID. In a NO TRANSACTION migration
-- every statement is its own transaction; VALIDATE takes a SHARE UPDATE
-- EXCLUSIVE lock, so writes continue during each scan.

-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
CREATE INDEX CONCURRENTLY IF NOT EXISTS outbound_ai_request_log_trace_idx
    ON outbound_ai_request_log (integration_id, trace_id, created_at) WHERE trace_id IS NOT NULL;
ALTER TABLE outbound_integrations VALIDATE CONSTRAINT outbound_integrations_ai_trace_limits_chk;
ALTER TABLE outbound_admission_leases VALIDATE CONSTRAINT outbound_admission_leases_ai_trace_id_chk;
ALTER TABLE outbound_ai_request_log VALIDATE CONSTRAINT outbound_ai_request_log_trace_chk;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS outbound_ai_request_log_trace_idx;
