-- filename: 20261124124546200_telemetry_coverage_clean_restarts_validate.sql
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- Validates the checks 20261124124546100 added NOT VALID.
ALTER TABLE request_telemetry_coverage VALIDATE CONSTRAINT request_telemetry_coverage_clean_stop_at_check;
ALTER TABLE request_analytics_app_gaps VALIDATE CONSTRAINT request_analytics_app_gaps_dropped_total_check;
ALTER TABLE request_telemetry_app_gaps VALIDATE CONSTRAINT request_telemetry_app_gaps_pending_since_check;
ALTER TABLE request_analytics_app_gaps VALIDATE CONSTRAINT request_analytics_app_gaps_pending_since_check;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
