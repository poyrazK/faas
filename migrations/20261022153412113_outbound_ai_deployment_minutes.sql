-- filename: 20261022153412113_outbound_ai_deployment_minutes.sql
-- ADR 20261022153155: a settled AI call from an app instance adds to its
-- deployment's row for the current UTC minute. Release gates of kind ai
-- compare a release's rows with its baseline's, and the API lists an app's
-- deployments from them. No foreign keys, like the route minutes
-- (ADR-1340), so a settlement never fails on this table; rows are pruned
-- after OutboundAIDeploymentStatsRetentionDays. A new table only.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS outbound_ai_deployment_minutes (
    deployment_id uuid NOT NULL,
    minute timestamptz NOT NULL,
    app_id uuid NOT NULL,
    calls bigint NOT NULL DEFAULT 0,
    error_calls bigint NOT NULL DEFAULT 0,
    flagged_calls bigint NOT NULL DEFAULT 0,
    cost_millicents bigint NOT NULL DEFAULT 0,
    input_tokens bigint NOT NULL DEFAULT 0,
    output_tokens bigint NOT NULL DEFAULT 0,
    ttfb_calls bigint NOT NULL DEFAULT 0,
    ttfb_ms_total bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (deployment_id, minute),
    CONSTRAINT outbound_ai_deployment_minutes_values_chk CHECK (
        calls >= 0 AND error_calls BETWEEN 0 AND calls AND flagged_calls BETWEEN 0 AND calls
        AND cost_millicents >= 0 AND input_tokens >= 0 AND output_tokens >= 0
        AND ttfb_calls BETWEEN 0 AND calls AND ttfb_ms_total >= 0)
);
CREATE INDEX IF NOT EXISTS outbound_ai_deployment_minutes_app ON outbound_ai_deployment_minutes (app_id, minute);
CREATE INDEX IF NOT EXISTS outbound_ai_deployment_minutes_minute ON outbound_ai_deployment_minutes (minute);
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DROP TABLE IF EXISTS outbound_ai_deployment_minutes;
-- +goose StatementEnd
