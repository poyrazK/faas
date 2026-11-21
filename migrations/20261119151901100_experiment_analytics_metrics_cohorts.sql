-- filename: 20261119151901100_experiment_analytics_metrics_cohorts.sql
-- migration-compat: expand
-- ADR 20261119102741: funnel and retention experiment metrics, CUPED and
-- analytics cohorts. Experiments gain their funnel or retention definition,
-- whether CUPED is on and the running minimum adjusted p-values; units gain
-- their pre-period covariate (NULL until filled). analytics_cohorts keeps an
-- environment's saved cohort queries and analytics_cohort_members the
-- customers of each cohort's last refresh, which the gateway stamps on
-- requests.
-- The widened experiment CHECK is added under a new name NOT VALID and
-- validated before the old one is dropped. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS metric_definition jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS cuped boolean NOT NULL DEFAULT true;
ALTER TABLE flag_experiments ADD COLUMN IF NOT EXISTS min_cuped_p_values jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementBegin
DO $experiments$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'flag_experiments'::regclass AND conname = 'flag_experiments_v3_chk') THEN
    ALTER TABLE flag_experiments ADD CONSTRAINT flag_experiments_v3_chk CHECK (
        key ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$'
        AND metric_event ~ '^[a-z][a-z0-9_.-]{0,63}$'
        AND metric_kind IN ('conversion', 'mean', 'funnel', 'retention')
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
        AND (status <> 'stopped' OR stopped_at IS NOT NULL)) NOT VALID;
  END IF;
END
$experiments$;
-- +goose StatementEnd

ALTER TABLE flag_experiments VALIDATE CONSTRAINT flag_experiments_v3_chk;
ALTER TABLE flag_experiments DROP CONSTRAINT IF EXISTS flag_experiments_v2_chk;

ALTER TABLE flag_experiment_units ADD COLUMN IF NOT EXISTS pre_value double precision;

CREATE TABLE IF NOT EXISTS analytics_cohorts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    key text NOT NULL,
    definition jsonb NOT NULL,
    members integer NOT NULL DEFAULT 0,
    truncated boolean NOT NULL DEFAULT false,
    refreshed_at timestamptz,
    refresh_requested_at timestamptz NOT NULL DEFAULT now(),
    refresh_error text NOT NULL DEFAULT '',
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT analytics_cohorts_env_key_uniq UNIQUE (environment_id, key),
    CONSTRAINT analytics_cohorts_chk CHECK (
        key ~ '^[a-z][a-z0-9_-]{0,63}$'
        AND jsonb_typeof(definition) = 'object'
        AND definition->>'kind' IN ('did', 'did_not', 'funnel')
        AND members >= 0
        AND length(refresh_error) <= 512
        AND length(created_by) BETWEEN 1 AND 256)
);
CREATE INDEX IF NOT EXISTS analytics_cohorts_refresh_idx ON analytics_cohorts (refresh_requested_at);

CREATE TABLE IF NOT EXISTS analytics_cohort_members (
    cohort_id uuid NOT NULL REFERENCES analytics_cohorts(id) ON DELETE CASCADE,
    customer_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    PRIMARY KEY (cohort_id, customer_id)
);
CREATE INDEX IF NOT EXISTS analytics_cohort_members_customer_idx ON analytics_cohort_members (customer_id);

-- +goose Down
DROP TABLE IF EXISTS analytics_cohort_members;
DROP TABLE IF EXISTS analytics_cohorts;
