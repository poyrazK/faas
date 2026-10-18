-- filename: 20261014141850964_app_log_drains_datadog_kind.sql

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-752: allow the datadog log-drain kind. apid and logdrain.New restrict
-- its target to the supported Datadog logs intakes.
ALTER TABLE app_log_drains DROP CONSTRAINT IF EXISTS app_log_drains_kind_chk;
ALTER TABLE app_log_drains ADD CONSTRAINT app_log_drains_kind_chk
  CHECK (kind = ANY (ARRAY['http_json'::text, 'otlp'::text, 'datadog'::text]));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The narrower check cannot hold datadog rows; rolling back removes them.
DELETE FROM app_log_drains WHERE kind = 'datadog';
ALTER TABLE app_log_drains DROP CONSTRAINT IF EXISTS app_log_drains_kind_chk;
ALTER TABLE app_log_drains ADD CONSTRAINT app_log_drains_kind_chk
  CHECK (kind = ANY (ARRAY['http_json'::text, 'otlp'::text]));
-- +goose StatementEnd
