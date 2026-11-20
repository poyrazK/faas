-- filename: 20261120042313100_experiment_units_layers_bandits.sql
-- migration-compat: expand
-- ADR 20261119173702: subject units, layer slices, bandit allocation, ratio
-- and percentile metrics. Experiments gain their unit (customer or
-- subject), their layer and traffic and the slice they hold while running,
-- their allocation and the bandit's last posterior and re-weight time, a
-- ratio metric's denominator event and a percentile metric's percentile.
-- Units gain, for subject units, the real customer and the subject hash
-- (customer_id is then a UUID derived from both), and a ratio metric's
-- folded denominator sum.
-- The widened experiment CHECK is added under a new name NOT VALID and
-- validated before the old one is dropped. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS unit text NOT NULL DEFAULT 'customer';
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS layer text NOT NULL DEFAULT '';
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS traffic integer NOT NULL DEFAULT 0;
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS layer_from integer NOT NULL DEFAULT 0;
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS layer_to integer NOT NULL DEFAULT 0;
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS allocation text NOT NULL DEFAULT 'fixed';
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS bandit jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS bandit_weighted_at timestamptz;
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS denominator_event text NOT NULL DEFAULT '';
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS percentile integer NOT NULL DEFAULT 0;

-- +goose StatementBegin
DO $experiments$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'flag_experiments'::regclass AND conname = 'flag_experiments_v4_chk') THEN
    ALTER TABLE flag_experiments ADD CONSTRAINT flag_experiments_v4_chk CHECK (
        key ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$'
        AND metric_event ~ '^[a-z][a-z0-9_.-]{0,63}$'
        AND metric_kind IN ('conversion', 'mean', 'funnel', 'retention', 'ratio', 'percentile')
        AND status IN ('draft', 'running', 'stopped')
        AND mixing_lift > 0 AND mixing_lift <= 1
        AND jsonb_typeof(weights) = 'object' AND jsonb_typeof(min_p_values) = 'object'
        AND jsonb_typeof(min_cuped_p_values) = 'object' AND jsonb_typeof(metric_definition) = 'object'
        AND jsonb_typeof(guardrails) = 'object'
        AND (breach IS NULL OR jsonb_typeof(breach) = 'object')
        AND (latency_threshold_ms IS NULL OR latency_threshold_ms >= 0)
        AND stop_reason IN ('', 'manual', 'guardrail_breach', 'shipped')
        AND (status = 'stopped' OR stop_reason = '')
        AND length(flag_key) BETWEEN 1 AND 128 AND length(rule_id) BETWEEN 1 AND 128
        AND length(control) BETWEEN 1 AND 128 AND length(created_by) BETWEEN 1 AND 256
        AND (status = 'draft' OR started_at IS NOT NULL)
        AND (status <> 'stopped' OR stopped_at IS NOT NULL)
        AND unit IN ('customer', 'subject')
        AND allocation IN ('fixed', 'bandit')
        AND (layer = '' OR layer ~ '^[a-z][a-z0-9_-]{0,63}$')
        AND traffic BETWEEN 0 AND 10000
        AND layer_from BETWEEN 0 AND 10000 AND layer_to BETWEEN 0 AND 10000 AND layer_from <= layer_to
        AND jsonb_typeof(bandit) = 'array'
        AND (denominator_event = '' OR denominator_event ~ '^[a-z][a-z0-9_.-]{0,63}$')
        AND percentile BETWEEN 0 AND 99) NOT VALID;
  END IF;
END
$experiments$;
-- +goose StatementEnd

ALTER TABLE flag_experiments VALIDATE CONSTRAINT flag_experiments_v4_chk;
ALTER TABLE flag_experiments DROP CONSTRAINT IF EXISTS flag_experiments_v3_chk;

ALTER TABLE flag_experiment_units ADD COLUMN IF NOT EXISTS unit_customer_id uuid;
ALTER TABLE flag_experiment_units ADD COLUMN IF NOT EXISTS subject_hash bytea;
ALTER TABLE flag_experiment_units ADD COLUMN IF NOT EXISTS den_sum bigint NOT NULL DEFAULT 0;

-- +goose StatementBegin
DO $units$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'flag_experiment_units'::regclass AND conname = 'flag_experiment_units_subject_chk') THEN
    ALTER TABLE flag_experiment_units ADD CONSTRAINT flag_experiment_units_subject_chk CHECK (
        (subject_hash IS NULL AND unit_customer_id IS NULL)
        OR (length(subject_hash) = 32 AND unit_customer_id IS NOT NULL)) NOT VALID;
  END IF;
END
$units$;
-- +goose StatementEnd

ALTER TABLE flag_experiment_units VALIDATE CONSTRAINT flag_experiment_units_subject_chk;

-- +goose Down
SELECT 1;
