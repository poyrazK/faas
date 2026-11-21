-- filename: 20261115164341806_flag_experiments.sql
-- ADR 20261115163113: flag experiments. Conversion events are folded into
-- one row per environment, event, customer and UTC day
-- (flag_conversion_daily) and swept with the plan's request telemetry
-- retention; flag_event_usage counts each account's monthly quota;
-- flag_event_names bounds distinct event names per environment.
-- flag_experiments and flag_experiment_units keep an experiment's state for
-- its whole lifetime, independent of telemetry retention.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS flag_event_usage (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    month date NOT NULL,
    accepted bigint NOT NULL DEFAULT 0,
    dropped bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, month),
    CONSTRAINT flag_event_usage_chk CHECK (accepted >= 0 AND dropped >= 0 AND month = date_trunc('month', month)::date)
);

CREATE TABLE IF NOT EXISTS flag_event_names (
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    name text NOT NULL,
    account_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (environment_id, name),
    CONSTRAINT flag_event_names_chk CHECK (name ~ '^[a-z][a-z0-9_.-]{0,63}$')
);

CREATE TABLE IF NOT EXISTS flag_conversion_daily (
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    event_name text NOT NULL,
    customer_id uuid NOT NULL,
    day date NOT NULL,
    account_id uuid NOT NULL,
    count bigint NOT NULL,
    value_sum bigint NOT NULL DEFAULT 0,
    first_at timestamptz NOT NULL,
    last_at timestamptz NOT NULL,
    PRIMARY KEY (environment_id, event_name, customer_id, day),
    CONSTRAINT flag_conversion_daily_chk CHECK (count > 0 AND first_at <= last_at)
);
CREATE INDEX IF NOT EXISTS flag_conversion_daily_account_day_idx ON flag_conversion_daily (account_id, day);

CREATE TABLE IF NOT EXISTS flag_experiments (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_id uuid NOT NULL,
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    key text NOT NULL,
    flag_key text NOT NULL,
    rule_id text NOT NULL,
    control text NOT NULL,
    metric_event text NOT NULL,
    metric_kind text NOT NULL DEFAULT 'conversion',
    status text NOT NULL DEFAULT 'draft',
    mixing_lift double precision NOT NULL,
    weights jsonb NOT NULL DEFAULT '{}'::jsonb,
    min_p_values jsonb NOT NULL DEFAULT '{}'::jsonb,
    telemetry_cursor timestamptz,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    stopped_at timestamptz,
    CONSTRAINT flag_experiments_key_unique UNIQUE (environment_id, key),
    CONSTRAINT flag_experiments_chk CHECK (
        key ~ '^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$'
        AND metric_event ~ '^[a-z][a-z0-9_.-]{0,63}$'
        AND metric_kind IN ('conversion')
        AND status IN ('draft', 'running', 'stopped')
        AND mixing_lift > 0 AND mixing_lift <= 1
        AND jsonb_typeof(weights) = 'object' AND jsonb_typeof(min_p_values) = 'object'
        AND length(flag_key) BETWEEN 1 AND 128 AND length(rule_id) BETWEEN 1 AND 128
        AND length(control) BETWEEN 1 AND 128 AND length(created_by) BETWEEN 1 AND 256
        AND (status = 'draft' OR started_at IS NOT NULL)
        AND (status <> 'stopped' OR stopped_at IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS flag_experiments_one_running_per_rule
    ON flag_experiments (environment_id, flag_key, rule_id) WHERE status = 'running';
CREATE INDEX IF NOT EXISTS flag_experiments_running_idx ON flag_experiments (id) WHERE status = 'running';

CREATE TABLE IF NOT EXISTS flag_experiment_units (
    experiment_id uuid NOT NULL REFERENCES flag_experiments(id) ON DELETE CASCADE,
    customer_id uuid NOT NULL,
    variant text NOT NULL,
    first_exposed_at timestamptz NOT NULL,
    converted_at timestamptz,
    crossed boolean NOT NULL DEFAULT false,
    PRIMARY KEY (experiment_id, customer_id),
    CONSTRAINT flag_experiment_units_chk CHECK (length(variant) BETWEEN 1 AND 128 AND (converted_at IS NULL OR converted_at >= first_exposed_at))
);

-- +goose Down
DROP TABLE IF EXISTS flag_experiment_units;
DROP TABLE IF EXISTS flag_experiments;
DROP TABLE IF EXISTS flag_conversion_daily;
DROP TABLE IF EXISTS flag_event_names;
DROP TABLE IF EXISTS flag_event_usage;
