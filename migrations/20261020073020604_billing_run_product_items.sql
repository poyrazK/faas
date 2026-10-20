-- filename: 20261020073020604_billing_run_product_items.sql

-- ADR-20261018212918 §4: a billing run with the product_customers source also
-- bills each Product Control customer's month. Their items live in their own
-- table, keyed by app and tenant, because a tenant can be a customer of several
-- Product Control apps and because a previous release must never read them as
-- platform tenant items. product_discovered_at records their discovery apart
-- from discovered_at, which a previous release sets without discovering them.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE billing_runs ADD COLUMN IF NOT EXISTS product_discovered_at timestamptz;
ALTER TABLE billing_runs DROP CONSTRAINT IF EXISTS billing_runs_sources_chk;
ALTER TABLE billing_runs ADD CONSTRAINT billing_runs_sources_chk CHECK (
  cardinality(sources) BETWEEN 1 AND 3 AND sources <@ ARRAY['app_consumers', 'platform_tenants', 'product_customers']::text[]);
ALTER TABLE billing_run_schedules DROP CONSTRAINT IF EXISTS billing_run_schedules_sources_chk;
ALTER TABLE billing_run_schedules ADD CONSTRAINT billing_run_schedules_sources_chk CHECK (
  cardinality(sources) BETWEEN 1 AND 3 AND sources <@ ARRAY['app_consumers', 'platform_tenants', 'product_customers']::text[]);

CREATE TABLE IF NOT EXISTS billing_run_product_items (
    run_id            uuid NOT NULL REFERENCES billing_runs(id) ON DELETE CASCADE,
    app_id            uuid NOT NULL,
    tenant_id         uuid NOT NULL,
    external_ref      text NOT NULL DEFAULT '',
    outcome           text NOT NULL DEFAULT 'pending',
    document_id       uuid,
    currency          text,
    amount_millicents bigint,
    invoice_id        text,
    exported_count    integer NOT NULL DEFAULT 0,
    reason            text,
    attempts          integer NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT billing_run_product_items_pkey PRIMARY KEY (run_id, app_id, tenant_id),
    CONSTRAINT billing_run_product_items_outcome_chk CHECK (outcome IN
      ('pending', 'drafted', 'finalized', 'exported', 'up_to_date', 'held', 'skipped', 'failed')),
    CONSTRAINT billing_run_product_items_currency_chk CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$'),
    CONSTRAINT billing_run_product_items_counts_chk CHECK (attempts >= 0 AND exported_count >= 0),
    CONSTRAINT billing_run_product_items_reason_chk CHECK (reason IS NULL OR char_length(reason) <= 1000),
    CONSTRAINT billing_run_product_items_external_ref_chk CHECK (char_length(external_ref) <= 256)
);
CREATE INDEX IF NOT EXISTS billing_run_product_items_pending_idx
  ON billing_run_product_items (run_id, next_attempt_at) WHERE outcome = 'pending';
COMMENT ON TABLE billing_run_product_items IS
  'One Product Control customer (app and platform tenant) of a billing run (ADR-20261018212918).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS billing_run_product_items;
DELETE FROM billing_run_schedules WHERE 'product_customers' = ANY(sources);
DELETE FROM billing_runs WHERE 'product_customers' = ANY(sources);
ALTER TABLE billing_run_schedules DROP CONSTRAINT IF EXISTS billing_run_schedules_sources_chk;
ALTER TABLE billing_run_schedules ADD CONSTRAINT billing_run_schedules_sources_chk CHECK (
  cardinality(sources) BETWEEN 1 AND 2 AND sources <@ ARRAY['app_consumers', 'platform_tenants']::text[]);
ALTER TABLE billing_runs DROP CONSTRAINT IF EXISTS billing_runs_sources_chk;
ALTER TABLE billing_runs ADD CONSTRAINT billing_runs_sources_chk CHECK (
  cardinality(sources) BETWEEN 1 AND 2 AND sources <@ ARRAY['app_consumers', 'platform_tenants']::text[]);
ALTER TABLE billing_runs DROP COLUMN IF EXISTS product_discovered_at;
-- +goose StatementEnd
