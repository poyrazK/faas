-- +goose Up
-- +goose StatementBegin

-- Provider-qualified identities prevent an account's customer/subscription
-- handles from one backend being reused after a deployment switches billing
-- providers. The legacy account columns remain as the active-provider cache
-- during the compatibility window.
CREATE TABLE IF NOT EXISTS billing_identities (
  account_id      uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  provider        text NOT NULL CHECK (provider IN ('stripe','paddle','polar')),
  customer_id     text NOT NULL CHECK (customer_id <> ''),
  subscription_id text NOT NULL DEFAULT '',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (account_id, provider),
  UNIQUE (provider, customer_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS billing_identities_provider_subscription_idx
  ON billing_identities (provider, subscription_id)
  WHERE subscription_id <> '';

-- A lifecycle interval ledger replaces the old "present at the minute tick =
-- resident for 60 seconds" approximation. State changes are already durable
-- Postgres writes, so a trigger captures short-lived instances even when they
-- start and stop between meterd ticks.
CREATE TABLE IF NOT EXISTS instance_billing_intervals (
  id          bigserial PRIMARY KEY,
  instance_id uuid NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
  started_at  timestamptz NOT NULL,
  ended_at    timestamptz,
  CHECK (ended_at IS NULL OR ended_at >= started_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS instance_billing_intervals_open_idx
  ON instance_billing_intervals (instance_id)
  WHERE ended_at IS NULL;

CREATE INDEX IF NOT EXISTS instance_billing_intervals_range_idx
  ON instance_billing_intervals (started_at, ended_at);

CREATE OR REPLACE FUNCTION capture_instance_billing_interval()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  old_billable boolean := false;
  new_billable boolean;
  changed_at timestamptz := clock_timestamp();
BEGIN
  new_billable := NEW.state IN ('waking','cold_booting','running','snapshotting','migrating')
                  AND COALESCE(NEW.mode, 'normal') <> 'mirror';
  IF TG_OP = 'UPDATE' THEN
    old_billable := OLD.state IN ('waking','cold_booting','running','snapshotting','migrating')
                    AND COALESCE(OLD.mode, 'normal') <> 'mirror';
  END IF;

  IF new_billable AND NOT old_billable THEN
    INSERT INTO instance_billing_intervals (instance_id, started_at)
    VALUES (NEW.id, CASE WHEN TG_OP = 'INSERT' THEN COALESCE(NEW.started_at, changed_at) ELSE changed_at END)
    ON CONFLICT (instance_id) WHERE ended_at IS NULL DO NOTHING;
  ELSIF old_billable AND NOT new_billable THEN
    UPDATE instance_billing_intervals
       SET ended_at = changed_at
     WHERE instance_id = NEW.id AND ended_at IS NULL;
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS instances_billing_interval_trigger ON instances;
CREATE TRIGGER instances_billing_interval_trigger
AFTER INSERT OR UPDATE OF state, mode ON instances
FOR EACH ROW EXECUTE FUNCTION capture_instance_billing_interval();

-- Begin tracking currently-resident instances at migration time. Starting at
-- now avoids inventing historical intervals or duplicating usage already
-- materialised by the legacy sampler.
INSERT INTO instance_billing_intervals (instance_id, started_at)
SELECT id, now()
  FROM instances
 WHERE state IN ('waking','cold_booting','running','snapshotting','migrating')
   AND COALESCE(mode, 'normal') <> 'mirror'
ON CONFLICT (instance_id) WHERE ended_at IS NULL DO NOTHING;

-- Existing installations are single-provider deployments. Known Stripe and
-- Paddle prefixes are deterministic; every other non-empty identifier is a
-- Polar UUID/handle because Polar is the default provider.
INSERT INTO billing_identities (account_id, provider, customer_id, subscription_id)
SELECT id,
       CASE
         WHEN left(provider_customer_id, 4) = 'cus_' THEN 'stripe'
         WHEN left(provider_customer_id, 4) = 'ctm_' THEN 'paddle'
         ELSE 'polar'
       END,
       provider_customer_id,
       COALESCE(stripe_subscription_item, '')
  FROM accounts
 WHERE COALESCE(provider_customer_id, '') <> ''
ON CONFLICT (account_id, provider) DO NOTHING;

-- Immutable billing terms and cumulative refund state make historical credit
-- calculations independent of a later plan change and make partial-refund
-- validation local instead of outsourcing it to the provider.
ALTER TABLE invoices
  ADD COLUMN IF NOT EXISTS provider_charge_id text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS plan text NOT NULL DEFAULT 'free',
  ADD COLUMN IF NOT EXISTS amount_refunded_cents bigint NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS credits_applied_cents bigint NOT NULL DEFAULT 0;

ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_plan_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_plan_check
  CHECK (plan IN ('free','hobby','pro','scale'));
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_amount_refunded_cents_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_amount_refunded_cents_check
  CHECK (amount_refunded_cents >= 0);
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_credits_applied_cents_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_credits_applied_cents_check
  CHECK (credits_applied_cents >= 0 AND credits_applied_cents <= amount_refunded_cents);

-- Establish a best available immutable snapshot for pre-migration invoices.
UPDATE invoices i
   SET plan = a.plan
  FROM accounts a
 WHERE a.id = i.account_id;

CREATE UNIQUE INDEX IF NOT EXISTS invoices_provider_charge_idx
  ON invoices (provider, provider_charge_id)
  WHERE provider_charge_id <> '';

CREATE TABLE IF NOT EXISTS invoice_refunds (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  invoice_id         uuid NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
  provider_refund_id text NOT NULL CHECK (provider_refund_id <> ''),
  idempotency_key    text NOT NULL CHECK (idempotency_key <> ''),
  amount_cents       bigint NOT NULL CHECK (amount_cents > 0),
  source             text NOT NULL CHECK (source IN ('operator','credit','webhook')),
  status             text NOT NULL DEFAULT '',
  created_at         timestamptz NOT NULL DEFAULT now(),
  UNIQUE (invoice_id, provider_refund_id),
  UNIQUE (invoice_id, idempotency_key)
);

-- Pusher-owned delivery receipts let meterd ask for all undelivered retained
-- usage instead of relying on a fixed 30-day replay horizon. Provider-owned
-- dedupe remains the second line of defence for crash-after-provider-success.
CREATE TABLE IF NOT EXISTS billing_usage_deliveries (
  provider      text NOT NULL CHECK (provider IN ('stripe','paddle','polar')),
  account_id    uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  window_start  timestamptz NOT NULL,
  mb_seconds    bigint NOT NULL CHECK (mb_seconds >= 0),
  delivered_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (provider, account_id, window_start)
);

CREATE INDEX IF NOT EXISTS billing_usage_deliveries_window_idx
  ON billing_usage_deliveries (window_start);

-- Backfill provider receipts where the old provider-specific dedupe tables
-- already prove success. The identity join disambiguates the historical table
-- shared by Stripe and Polar.
INSERT INTO billing_usage_deliveries (provider, account_id, window_start, mb_seconds)
SELECT bi.provider, d.account_id, d.hour, 0
  FROM stripe_push_dedupe d
  JOIN billing_identities bi ON bi.account_id = d.account_id
 WHERE bi.provider IN ('stripe','polar')
ON CONFLICT DO NOTHING;

INSERT INTO billing_usage_deliveries (provider, account_id, window_start, mb_seconds)
SELECT 'paddle', d.account_id, d.window_start, COALESCE(d.pushed_mb_seconds, 0)
  FROM paddle_overage_dedupe d
 WHERE d.window_start IS NOT NULL AND d.state = 'completed'
ON CONFLICT DO NOTHING;

-- Correct the historical comment's financial-model typo: one cent per GB-h,
-- not one hundred cents. This is documentation-only; runtime math was correct.
COMMENT ON TABLE account_credits IS
  'Operator-issued EUR-cent credits; one credit cent offsets one GB-RAM-hour of overage at the current public rate.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS billing_usage_deliveries;
DROP TABLE IF EXISTS invoice_refunds;
DROP INDEX IF EXISTS invoices_provider_charge_idx;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_credits_applied_cents_check;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_amount_refunded_cents_check;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_plan_check;
ALTER TABLE invoices
  DROP COLUMN IF EXISTS credits_applied_cents,
  DROP COLUMN IF EXISTS amount_refunded_cents,
  DROP COLUMN IF EXISTS plan,
  DROP COLUMN IF EXISTS provider_charge_id;
DROP INDEX IF EXISTS billing_identities_provider_subscription_idx;
DROP TRIGGER IF EXISTS instances_billing_interval_trigger ON instances;
DROP FUNCTION IF EXISTS capture_instance_billing_interval();
DROP TABLE IF EXISTS instance_billing_intervals;
DROP TABLE IF EXISTS billing_identities;
-- +goose StatementEnd
