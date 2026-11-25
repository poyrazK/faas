-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Keep accepting v5 records during a rolling scheduler upgrade. Readers
-- must require v6 before using the trust fingerprint as evidence.
ALTER TABLE data_api_inspection_executions
 DROP CONSTRAINT data_api_inspection_executions_protocol_version_check,
 ADD CONSTRAINT data_api_inspection_executions_protocol_version_check
 CHECK (protocol_version IN (5, 6));

-- +goose Down
-- Refuse rollback while v6 evidence still exists rather than discard it.
ALTER TABLE data_api_inspection_executions
 DROP CONSTRAINT data_api_inspection_executions_protocol_version_check,
 ADD CONSTRAINT data_api_inspection_executions_protocol_version_check
 CHECK (protocol_version = 5);
