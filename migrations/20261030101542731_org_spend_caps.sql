-- filename: 20261030101542731_org_spend_caps.sql

-- ADR-20261030093512: an organization that owns its resources is billed as
-- its own subject, so it gets its own spend cap. orgs.overage_cap_cents mirrors
-- accounts.overage_cap_cents (NULL = no cap, 0 = stop at the allowance), and a
-- spend cap hold may name an organization: its key moves from accounts to
-- resource_namespaces, which holds every account and organization ID.
-- migration-compat: expand
--
-- The CHECK and the new foreign key are added NOT VALID and validated in
-- their own statements; a VALIDATE takes SHARE UPDATE EXCLUSIVE, so orgs and
-- holds keep being written during the scans. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE orgs ADD COLUMN IF NOT EXISTS overage_cap_cents bigint;

-- +goose StatementBegin
DO $cap$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'orgs'::regclass AND conname = 'orgs_overage_cap_cents_chk') THEN
    ALTER TABLE orgs ADD CONSTRAINT orgs_overage_cap_cents_chk
      CHECK (overage_cap_cents IS NULL OR overage_cap_cents >= 0) NOT VALID;
  END IF;
END
$cap$;
-- +goose StatementEnd

ALTER TABLE orgs VALIDATE CONSTRAINT orgs_overage_cap_cents_chk;

COMMENT ON COLUMN orgs.overage_cap_cents IS
    'Monthly spend cap in cents for an organization that owns its resources; NULL = no cap (ADR-20261030093512).';

-- +goose StatementBegin
DO $hold$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_spend_cap_holds'::regclass AND conname = 'account_spend_cap_holds_subject_fkey') THEN
    ALTER TABLE account_spend_cap_holds ADD CONSTRAINT account_spend_cap_holds_subject_fkey
      FOREIGN KEY (account_id) REFERENCES resource_namespaces(id) ON DELETE CASCADE NOT VALID;
  END IF;
END
$hold$;
-- +goose StatementEnd

ALTER TABLE account_spend_cap_holds VALIDATE CONSTRAINT account_spend_cap_holds_subject_fkey;

ALTER TABLE account_spend_cap_holds DROP CONSTRAINT IF EXISTS account_spend_cap_holds_account_id_fkey;

-- +goose Down
SET lock_timeout = '5s';
DELETE FROM account_spend_cap_holds h WHERE NOT EXISTS (SELECT 1 FROM accounts a WHERE a.id = h.account_id);
-- +goose StatementBegin
DO $hold$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'account_spend_cap_holds'::regclass AND conname = 'account_spend_cap_holds_account_id_fkey') THEN
    ALTER TABLE account_spend_cap_holds ADD CONSTRAINT account_spend_cap_holds_account_id_fkey
      FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
  END IF;
END
$hold$;
-- +goose StatementEnd
ALTER TABLE account_spend_cap_holds DROP CONSTRAINT IF EXISTS account_spend_cap_holds_subject_fkey;
ALTER TABLE orgs DROP CONSTRAINT IF EXISTS orgs_overage_cap_cents_chk;
ALTER TABLE orgs DROP COLUMN IF EXISTS overage_cap_cents;
