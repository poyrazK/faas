-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-1746. Protocol-1 registration alone proves only app compute support.
-- A current binary attests account support for its exact native incarnation.
-- A legacy upsert omits this field: it cannot change incarnation after an
-- attested owner without violating the constraint and failing daemon startup.
ALTER TABLE financial_compute_owners ADD COLUMN IF NOT EXISTS account_compute_incarnation uuid;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='financial_compute_owners'::regclass AND conname='financial_compute_owner_account_incarnation_check') THEN
  ALTER TABLE financial_compute_owners ADD CONSTRAINT financial_compute_owner_account_incarnation_check
 CHECK (account_compute_incarnation IS NULL OR account_compute_incarnation=incarnation);
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE financial_compute_owners DROP COLUMN account_compute_incarnation;

-- migration-compat: contract
-- Financial funding rollout coordinates scheduler, native-owner and meter
-- capabilities with the USD gate; this batch requires a full release.
