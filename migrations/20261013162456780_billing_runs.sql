-- filename: 20261013162456780_billing_runs.sql

-- ADR-1301: month-end billing runs. A run bills one ended UTC month for
-- every customer with billable usage; its items record each customer's
-- outcome. One queued or running run per account; a schedule starts one run
-- per month after month end plus delay_hours.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS billing_runs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id      uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    period_start    timestamptz NOT NULL,
    period_end      timestamptz NOT NULL,
    sources         text[] NOT NULL,
    finalize        boolean NOT NULL,
    export_provider text NOT NULL,
    hold_on_gaps    boolean NOT NULL,
    trigger         text NOT NULL,
    status          text NOT NULL DEFAULT 'queued',
    discovered_at   timestamptz,
    lease_owner     text,
    lease_until     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    completed_at    timestamptz,
    CONSTRAINT billing_runs_month_chk CHECK (
      period_start = date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
      AND period_end = (date_trunc('month', period_start AT TIME ZONE 'UTC') + interval '1 month') AT TIME ZONE 'UTC'),
    CONSTRAINT billing_runs_sources_chk CHECK (
      cardinality(sources) BETWEEN 1 AND 2 AND sources <@ ARRAY['app_consumers', 'platform_tenants']::text[]),
    CONSTRAINT billing_runs_export_chk CHECK (export_provider IN ('none', 'stripe')),
    CONSTRAINT billing_runs_export_finalize_chk CHECK (export_provider = 'none' OR finalize),
    CONSTRAINT billing_runs_trigger_chk CHECK (trigger IN ('api', 'schedule')),
    CONSTRAINT billing_runs_status_chk CHECK (status IN ('queued', 'running', 'completed')),
    CONSTRAINT billing_runs_completed_chk CHECK ((status = 'completed') = (completed_at IS NOT NULL)),
    CONSTRAINT billing_runs_lease_chk CHECK ((lease_owner IS NULL) = (lease_until IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS billing_runs_one_active_idx
  ON billing_runs (account_id) WHERE status IN ('queued', 'running');
CREATE UNIQUE INDEX IF NOT EXISTS billing_runs_scheduled_month_idx
  ON billing_runs (account_id, period_start) WHERE trigger = 'schedule';
CREATE INDEX IF NOT EXISTS billing_runs_account_idx ON billing_runs (account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS billing_runs_claim_idx
  ON billing_runs (created_at) WHERE status IN ('queued', 'running');

CREATE TABLE IF NOT EXISTS billing_run_items (
    run_id            uuid NOT NULL REFERENCES billing_runs(id) ON DELETE CASCADE,
    source_kind       text NOT NULL,
    source_id         uuid NOT NULL,
    app_id            uuid,
    external_ref      text NOT NULL DEFAULT '',
    outcome           text NOT NULL DEFAULT 'pending',
    statement_id      uuid,
    revision          integer,
    currency          text,
    amount_millicents bigint,
    completeness      text,
    invoice_id        text,
    exported_count    integer NOT NULL DEFAULT 0,
    reason            text,
    attempts          integer NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT billing_run_items_pkey PRIMARY KEY (run_id, source_kind, source_id),
    CONSTRAINT billing_run_items_source_chk CHECK (source_kind IN ('app_consumer', 'platform_tenant')),
    CONSTRAINT billing_run_items_app_chk CHECK ((source_kind = 'app_consumer') = (app_id IS NOT NULL)),
    CONSTRAINT billing_run_items_outcome_chk CHECK (outcome IN
      ('pending', 'drafted', 'finalized', 'exported', 'up_to_date', 'held', 'skipped', 'failed')),
    CONSTRAINT billing_run_items_completeness_chk CHECK (completeness IS NULL OR completeness IN
      ('verified', 'partial', 'gaps_detected', 'unverifiable')),
    CONSTRAINT billing_run_items_currency_chk CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$'),
    CONSTRAINT billing_run_items_counts_chk CHECK (attempts >= 0 AND exported_count >= 0 AND (revision IS NULL OR revision >= 1)),
    CONSTRAINT billing_run_items_reason_chk CHECK (reason IS NULL OR char_length(reason) <= 1000),
    CONSTRAINT billing_run_items_external_ref_chk CHECK (char_length(external_ref) <= 256)
);
CREATE INDEX IF NOT EXISTS billing_run_items_pending_idx
  ON billing_run_items (run_id, next_attempt_at) WHERE outcome = 'pending';

CREATE TABLE IF NOT EXISTS billing_run_schedules (
    account_id         uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    sources            text[] NOT NULL,
    finalize           boolean NOT NULL,
    export_provider    text NOT NULL,
    hold_on_gaps       boolean NOT NULL,
    delay_hours        integer NOT NULL,
    next_period_start  timestamptz NOT NULL,
    next_run_at        timestamptz NOT NULL,
    last_period_start  timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT billing_run_schedules_sources_chk CHECK (
      cardinality(sources) BETWEEN 1 AND 2 AND sources <@ ARRAY['app_consumers', 'platform_tenants']::text[]),
    CONSTRAINT billing_run_schedules_export_chk CHECK (export_provider IN ('none', 'stripe')),
    CONSTRAINT billing_run_schedules_export_finalize_chk CHECK (export_provider = 'none' OR finalize),
    CONSTRAINT billing_run_schedules_delay_chk CHECK (delay_hours BETWEEN 1 AND 240),
    CONSTRAINT billing_run_schedules_next_month_chk CHECK (
      next_period_start = date_trunc('month', next_period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
      AND next_run_at > next_period_start),
    CONSTRAINT billing_run_schedules_last_month_chk CHECK (last_period_start IS NULL OR
      last_period_start = date_trunc('month', last_period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
);
CREATE INDEX IF NOT EXISTS billing_run_schedules_due_idx ON billing_run_schedules (next_run_at);

COMMENT ON TABLE billing_runs IS
  'Month-end billing runs (ADR-1301): one ended UTC month billed for every customer with billable usage; worker-leased.';
COMMENT ON TABLE billing_run_items IS
  'One customer of a billing run: the statement drafted, finalized or exported for it, or why it was held, skipped or failed.';
COMMENT ON TABLE billing_run_schedules IS
  'Per-account schedule that starts a billing run for each month after month end plus delay_hours.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS billing_run_schedules;
DROP TABLE IF EXISTS billing_run_items;
DROP TABLE IF EXISTS billing_runs;
-- +goose StatementEnd
