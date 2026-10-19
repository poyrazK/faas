-- migration-compat: expand
-- filename: 20261019072506758_request_telemetry_content_outcome_validate.sql

-- ADR-1199: validate the widened content outcome check that
-- 20261016170649001 added NOT VALID. In its own transaction VALIDATE takes a
-- SHARE UPDATE EXCLUSIVE lock, so request telemetry keeps taking writes
-- during the scan.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE request_telemetry VALIDATE CONSTRAINT request_telemetry_content_outcome_check;

-- +goose Down
SELECT 1;
