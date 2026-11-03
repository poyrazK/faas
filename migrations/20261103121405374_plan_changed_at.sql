-- filename: 20261103121405374_plan_changed_at.sql

-- ADR-20261103121407 (metering audit round 3).
-- accounts.plan_changed_at and orgs.plan_changed_at: when the billing subject's
-- plan last changed. meterd records a month's plan history by observing the
-- plan once a minute and stamped each change with the minute it noticed it,
-- so a change made while meterd was down or catching up was dated hours late
-- and those hours got the old plan's allowance, Free status and cap. meterd
-- now dates a newly observed plan from this column. A trigger stamps it on
-- every path that changes plan, including the previous release's writes.
-- Existing rows are not backfilled: a NULL keeps the observed minute.
-- migration-compat: expand
-- Nullable columns without a default are catalog-only changes, and the CHECKs
-- are added NOT VALID and validated separately (SHARE UPDATE EXCLUSIVE), so
-- both tables keep being written. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE accounts ADD COLUMN IF NOT EXISTS plan_changed_at timestamptz;
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS plan_changed_at timestamptz;

-- +goose StatementBegin
DO $plan_changed$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'accounts'::regclass AND conname = 'accounts_plan_changed_at_chk') THEN
    ALTER TABLE accounts ADD CONSTRAINT accounts_plan_changed_at_chk
      CHECK (plan_changed_at IS NULL OR isfinite(plan_changed_at)) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'orgs'::regclass AND conname = 'orgs_plan_changed_at_chk') THEN
    ALTER TABLE orgs ADD CONSTRAINT orgs_plan_changed_at_chk
      CHECK (plan_changed_at IS NULL OR isfinite(plan_changed_at)) NOT VALID;
  END IF;
END
$plan_changed$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION stamp_plan_changed_at() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NEW.plan IS DISTINCT FROM OLD.plan THEN
  NEW.plan_changed_at := clock_timestamp();
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS accounts_plan_changed_at ON accounts;
CREATE TRIGGER accounts_plan_changed_at BEFORE UPDATE OF plan ON accounts
    FOR EACH ROW EXECUTE FUNCTION stamp_plan_changed_at();
DROP TRIGGER IF EXISTS orgs_plan_changed_at ON orgs;
CREATE TRIGGER orgs_plan_changed_at BEFORE UPDATE OF plan ON orgs
    FOR EACH ROW EXECUTE FUNCTION stamp_plan_changed_at();

ALTER TABLE accounts VALIDATE CONSTRAINT accounts_plan_changed_at_chk;
ALTER TABLE orgs VALIDATE CONSTRAINT orgs_plan_changed_at_chk;

COMMENT ON COLUMN accounts.plan_changed_at IS
    'When the plan last changed; meterd dates a newly observed plan from it (ADR-20261103121407).';
COMMENT ON COLUMN orgs.plan_changed_at IS
    'When the plan last changed; meterd dates a newly observed plan from it (ADR-20261103121407).';

-- +goose Down
SET lock_timeout = '5s';
DROP TRIGGER IF EXISTS orgs_plan_changed_at ON orgs;
DROP TRIGGER IF EXISTS accounts_plan_changed_at ON accounts;
DROP FUNCTION IF EXISTS stamp_plan_changed_at();
ALTER TABLE orgs DROP CONSTRAINT IF EXISTS orgs_plan_changed_at_chk;
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_plan_changed_at_chk;
ALTER TABLE orgs DROP COLUMN IF EXISTS plan_changed_at;
ALTER TABLE accounts DROP COLUMN IF EXISTS plan_changed_at;
