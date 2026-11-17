-- filename: 20261117064320100_flag_experiment_guardrails.sql
-- migration-compat: expand
-- ADR 20261116190208: flag experiment guardrails, mean metrics and stop
-- reasons. Units gain per-customer request, server error, latency-tested and
-- slow request counts and an accumulated metric value; experiments gain
-- their guardrail settings, the fixed p95 latency threshold, the stop reason
-- and breach, and the last UTC day folded into unit values.
-- flag_experiment_latency keeps each arm's latency histogram (5% log
-- buckets) for the experiment's lifetime, like the unit rows.
-- The widened experiment CHECK is added under a new name NOT VALID and
-- validated before the old one is dropped. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS guardrails jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS latency_threshold_ms integer;
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS stop_reason text NOT NULL DEFAULT '';
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS breach jsonb;
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS value_through_day date;

-- +goose StatementBegin
DO $experiments$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'flag_experiments'::regclass AND conname = 'flag_experiments_v2_chk') THEN
    ALTER TABLE flag_experiments ADD CONSTRAINT flag_experiments_v2_chk CHECK (
        key ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$'
        AND metric_event ~ '^[a-z][a-z0-9_.-]{0,63}$'
        AND metric_kind IN ('conversion', 'mean')
        AND status IN ('draft', 'running', 'stopped')
        AND mixing_lift > 0 AND mixing_lift <= 1
        AND jsonb_typeof(weights) = 'object' AND jsonb_typeof(min_p_values) = 'object'
        AND jsonb_typeof(guardrails) = 'object'
        AND (breach IS NULL OR jsonb_typeof(breach) = 'object')
        AND (latency_threshold_ms IS NULL OR latency_threshold_ms >= 0)
        AND stop_reason IN ('', 'manual', 'guardrail_breach', 'shipped')
        AND (status = 'stopped' OR stop_reason = '')
        AND length(flag_key) BETWEEN 1 AND 128 AND length(rule_id) BETWEEN 1 AND 128
        AND length(control) BETWEEN 1 AND 128 AND length(created_by) BETWEEN 1 AND 256
        AND (status = 'draft' OR started_at IS NOT NULL)
        AND (status <> 'stopped' OR stopped_at IS NOT NULL)) NOT VALID;
  END IF;
END
$experiments$;
-- +goose StatementEnd

ALTER TABLE flag_experiments VALIDATE CONSTRAINT flag_experiments_v2_chk;
ALTER TABLE flag_experiments DROP CONSTRAINT IF EXISTS flag_experiments_chk;

ALTER TABLE flag_experiment_units ADD COLUMN IF NOT EXISTS requests bigint NOT NULL DEFAULT 0;
ALTER TABLE flag_experiment_units ADD COLUMN IF NOT EXISTS errors bigint NOT NULL DEFAULT 0;
ALTER TABLE flag_experiment_units ADD COLUMN IF NOT EXISTS latency_requests bigint NOT NULL DEFAULT 0;
ALTER TABLE flag_experiment_units ADD COLUMN IF NOT EXISTS slow_requests bigint NOT NULL DEFAULT 0;
ALTER TABLE flag_experiment_units ADD COLUMN IF NOT EXISTS value_sum bigint NOT NULL DEFAULT 0;

-- +goose StatementBegin
DO $units$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'flag_experiment_units'::regclass AND conname = 'flag_experiment_units_counts_chk') THEN
    ALTER TABLE flag_experiment_units ADD CONSTRAINT flag_experiment_units_counts_chk CHECK (
        requests >= 0 AND errors BETWEEN 0 AND requests
        AND latency_requests BETWEEN 0 AND requests AND slow_requests BETWEEN 0 AND latency_requests) NOT VALID;
  END IF;
END
$units$;
-- +goose StatementEnd

ALTER TABLE flag_experiment_units VALIDATE CONSTRAINT flag_experiment_units_counts_chk;

CREATE TABLE IF NOT EXISTS flag_experiment_latency (
    experiment_id uuid NOT NULL REFERENCES flag_experiments(id) ON DELETE CASCADE,
    variant text NOT NULL,
    bucket smallint NOT NULL,
    requests bigint NOT NULL,
    PRIMARY KEY (experiment_id, variant, bucket),
    CONSTRAINT flag_experiment_latency_chk CHECK (length(variant) BETWEEN 1 AND 128 AND bucket >= 0 AND requests > 0)
);

-- +goose Down
SET lock_timeout = '5s';
DROP TABLE IF EXISTS flag_experiment_latency;
ALTER TABLE flag_experiment_units DROP CONSTRAINT IF EXISTS flag_experiment_units_counts_chk;
ALTER TABLE flag_experiment_units DROP COLUMN IF EXISTS value_sum;
ALTER TABLE flag_experiment_units DROP COLUMN IF EXISTS slow_requests;
ALTER TABLE flag_experiment_units DROP COLUMN IF EXISTS latency_requests;
ALTER TABLE flag_experiment_units DROP COLUMN IF EXISTS errors;
ALTER TABLE flag_experiment_units DROP COLUMN IF EXISTS requests;
