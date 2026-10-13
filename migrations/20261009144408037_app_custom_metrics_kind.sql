-- filename: 20261009144408037_app_custom_metrics_kind.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-745: OTLP ingestion stores cumulative counters alongside ADR-202 gauges.
-- Existing rows are gauges. Counters are charted with rate() and never used
-- as a scaling signal.
ALTER TABLE app_custom_metrics
  ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'gauge';
ALTER TABLE app_custom_metrics DROP CONSTRAINT IF EXISTS app_custom_metrics_kind_chk;
ALTER TABLE app_custom_metrics ADD CONSTRAINT app_custom_metrics_kind_chk
  CHECK (kind = ANY (ARRAY['gauge'::text, 'counter'::text]));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Counters are not valid ADR-202 gauges; rolling back removes them.
DELETE FROM app_custom_metrics WHERE kind = 'counter';
ALTER TABLE app_custom_metrics DROP CONSTRAINT IF EXISTS app_custom_metrics_kind_chk;
ALTER TABLE app_custom_metrics DROP COLUMN IF EXISTS kind;
-- +goose StatementEnd
