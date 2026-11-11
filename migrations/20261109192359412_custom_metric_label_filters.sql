-- filename: 20261109192359412_custom_metric_label_filters.sql
-- migration-compat: expand
-- ADR-20261108160000 slice C2: a custom_metric alert rule and a
-- custom_metric or metric_change release gate may select one labelled
-- series of their metric instead of its aggregate. The filter is the
-- series' exact label set as a JSON object of strings; NULL reads the
-- aggregate. Both columns are nullable with no default (catalog-only); the
-- CHECKs are added NOT VALID and validated separately. Every step is
-- re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS custom_metric_labels jsonb;
ALTER TABLE app_release_gates ADD COLUMN IF NOT EXISTS metric_labels jsonb;

-- +goose StatementBegin
DO $labels$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'alert_rules'::regclass AND conname = 'alert_rules_custom_metric_labels_chk') THEN
    ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_custom_metric_labels_chk
      CHECK (custom_metric_labels IS NULL OR (metric = 'custom_metric' AND jsonb_typeof(custom_metric_labels) = 'object')) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_release_gates'::regclass AND conname = 'app_release_gates_metric_labels_chk') THEN
    ALTER TABLE app_release_gates ADD CONSTRAINT app_release_gates_metric_labels_chk
      CHECK (metric_labels IS NULL OR (kind IN ('custom_metric', 'metric_change') AND jsonb_typeof(metric_labels) = 'object')) NOT VALID;
  END IF;
END
$labels$;
-- +goose StatementEnd

ALTER TABLE alert_rules VALIDATE CONSTRAINT alert_rules_custom_metric_labels_chk;
ALTER TABLE app_release_gates VALIDATE CONSTRAINT app_release_gates_metric_labels_chk;

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE app_release_gates DROP CONSTRAINT IF EXISTS app_release_gates_metric_labels_chk;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_custom_metric_labels_chk;
ALTER TABLE app_release_gates DROP COLUMN IF EXISTS metric_labels;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS custom_metric_labels;
