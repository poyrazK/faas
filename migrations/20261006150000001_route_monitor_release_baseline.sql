-- filename: 20261006150000001_route_monitor_release_baseline.sql

-- +goose Up
-- +goose StatementBegin
-- Retain only sanitized metadata from a deployment after a healthy, fully
-- serving route-monitor evaluation. Incidents snapshot this value on opening.
ALTER TABLE route_monitors
 ADD COLUMN IF NOT EXISTS last_healthy_deployment jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE route_monitors
 DROP CONSTRAINT IF EXISTS route_monitors_last_healthy_deployment_check;
ALTER TABLE route_monitors
 ADD CONSTRAINT route_monitors_last_healthy_deployment_check
 CHECK (jsonb_typeof(last_healthy_deployment) = 'object' AND octet_length(last_healthy_deployment::text) <= 2048);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Preserve release provenance already embedded in saved incidents.
SELECT 1;
-- +goose StatementEnd
