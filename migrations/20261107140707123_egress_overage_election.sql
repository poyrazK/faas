-- filename: 20261107140707123_egress_overage_election.sql

-- ADR-20261104204500 (egress overage opt-in).
-- accounts/orgs.egress_overage_enabled_at and egress_overage_disabled_at: the
-- billing subject's election to pay for application egress above its plan's
-- monthly allowance. Without it every plan stops at the allowance
-- (ADR-20261104090000). meterd bills an egress hour only when it starts in
-- [enabled_at, disabled_at), so turning the election off cannot drop overage
-- already sent; turning it back on clears disabled_at and keeps enabled_at.
-- migration-compat: expand
-- Nullable columns without a default are catalog-only changes, and the CHECKs
-- are added NOT VALID and validated separately (SHARE UPDATE EXCLUSIVE), so
-- both tables keep being written. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE accounts ADD COLUMN IF NOT EXISTS egress_overage_enabled_at timestamptz;
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS egress_overage_disabled_at timestamptz;
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS egress_overage_enabled_at timestamptz;
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS egress_overage_disabled_at timestamptz;

-- +goose StatementBegin
DO $egress_overage$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'accounts'::regclass AND conname = 'accounts_egress_overage_chk') THEN
    ALTER TABLE accounts ADD CONSTRAINT accounts_egress_overage_chk
      CHECK ((egress_overage_enabled_at IS NULL OR isfinite(egress_overage_enabled_at))
         AND (egress_overage_disabled_at IS NULL
              OR (egress_overage_enabled_at IS NOT NULL AND isfinite(egress_overage_disabled_at)
                  AND egress_overage_disabled_at >= egress_overage_enabled_at))) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'orgs'::regclass AND conname = 'orgs_egress_overage_chk') THEN
    ALTER TABLE orgs ADD CONSTRAINT orgs_egress_overage_chk
      CHECK ((egress_overage_enabled_at IS NULL OR isfinite(egress_overage_enabled_at))
         AND (egress_overage_disabled_at IS NULL
              OR (egress_overage_enabled_at IS NOT NULL AND isfinite(egress_overage_disabled_at)
                  AND egress_overage_disabled_at >= egress_overage_enabled_at))) NOT VALID;
  END IF;
END
$egress_overage$;
-- +goose StatementEnd

ALTER TABLE accounts VALIDATE CONSTRAINT accounts_egress_overage_chk;
ALTER TABLE orgs VALIDATE CONSTRAINT orgs_egress_overage_chk;

COMMENT ON COLUMN accounts.egress_overage_enabled_at IS
    'When the subject elected to pay for egress above its allowance; NULL = never (ADR-20261104204500).';
COMMENT ON COLUMN accounts.egress_overage_disabled_at IS
    'When the egress overage election ended; NULL while it is on (ADR-20261104204500).';
COMMENT ON COLUMN orgs.egress_overage_enabled_at IS
    'When the subject elected to pay for egress above its allowance; NULL = never (ADR-20261104204500).';
COMMENT ON COLUMN orgs.egress_overage_disabled_at IS
    'When the egress overage election ended; NULL while it is on (ADR-20261104204500).';

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE orgs DROP CONSTRAINT IF EXISTS orgs_egress_overage_chk;
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_egress_overage_chk;
ALTER TABLE orgs DROP COLUMN IF EXISTS egress_overage_disabled_at;
ALTER TABLE orgs DROP COLUMN IF EXISTS egress_overage_enabled_at;
ALTER TABLE accounts DROP COLUMN IF EXISTS egress_overage_disabled_at;
ALTER TABLE accounts DROP COLUMN IF EXISTS egress_overage_enabled_at;
