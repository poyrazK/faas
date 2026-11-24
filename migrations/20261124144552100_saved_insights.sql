-- filename: 20261124144552100_saved_insights.sql
-- migration-compat: expand
-- ADR-20261122190000: saved insights. saved_insights keeps an environment's
-- named analytics queries (events, funnel or retention) with a relative
-- range, so anyone who can read the project can run them again. A saved
-- insight may send a scheduled digest and a threshold alert to the
-- account's notification channels; meterd claims due rows (next_check_at,
-- lease_until), runs them and records the value and alert state. The table
-- is new, so the previous release neither reads nor writes it. Re-runnable.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS saved_insights (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    key text NOT NULL,
    name text NOT NULL,
    kind text NOT NULL,
    query jsonb NOT NULL,
    range_days integer NOT NULL,
    digest text NOT NULL DEFAULT '',
    alert_comparison text NOT NULL DEFAULT '',
    alert_threshold double precision NOT NULL DEFAULT 0,
    channel_ids uuid[] NOT NULL DEFAULT '{}',
    alert_state text NOT NULL DEFAULT 'ok',
    last_value double precision,
    last_run_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    next_digest_at timestamptz,
    next_check_at timestamptz,
    lease_until timestamptz,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT saved_insights_env_key_uniq UNIQUE (environment_id, key),
    CONSTRAINT saved_insights_chk CHECK (
        key ~ '^[a-z][a-z0-9_-]{0,63}$'
        AND length(name) BETWEEN 1 AND 128
        AND kind IN ('events', 'funnel', 'retention')
        AND jsonb_typeof(query) = 'object'
        AND range_days BETWEEN 1 AND 400
        AND digest IN ('', 'daily', 'weekly')
        AND alert_comparison IN ('', 'gt', 'gte', 'lt', 'lte')
        AND alert_threshold = alert_threshold
        AND cardinality(channel_ids) <= 5
        AND alert_state IN ('ok', 'firing')
        AND length(last_error) <= 512
        AND length(created_by) BETWEEN 1 AND 256
        AND ((digest = '' AND alert_comparison = '') = (next_check_at IS NULL)))
);
COMMENT ON TABLE saved_insights IS 'Named analytics queries per environment; digests and threshold alerts run by meterd (ADR-20261122190000).';

CREATE INDEX IF NOT EXISTS saved_insights_due_idx
    ON saved_insights (next_check_at) WHERE next_check_at IS NOT NULL;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS saved_insights;
