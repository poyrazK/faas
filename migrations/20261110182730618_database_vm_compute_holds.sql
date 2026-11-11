-- filename: 20261110182730618_database_vm_compute_holds.sql

-- ADR-1027 amendment 2026-11-10 (compute caps and holds): apid holds a
-- native database (the delete grace period, or the compute cap sweep) and
-- gives it a monthly compute quota (ADR-20261025094915). schedd parks a held
-- database and refuses its wakes; it does the same for a database whose
-- compute this UTC month reached its quota. Only schedd's database
-- scheduler reads or writes the columns; an older schedd ignores them.
-- migration-compat: expand
--
-- The CHECK is added NOT VALID and validated in its own statement, so
-- databases keep being written. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE database_vms
  ADD COLUMN IF NOT EXISTS held boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS compute_quota_unit_seconds bigint;

-- +goose StatementBegin
DO $holds$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'database_vms'::regclass AND conname = 'database_vms_compute_quota_check') THEN
    ALTER TABLE database_vms ADD CONSTRAINT database_vms_compute_quota_check
      CHECK (compute_quota_unit_seconds IS NULL OR compute_quota_unit_seconds > 0) NOT VALID;
  END IF;
END
$holds$;
-- +goose StatementEnd

ALTER TABLE database_vms VALIDATE CONSTRAINT database_vms_compute_quota_check;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE database_vms DROP CONSTRAINT IF EXISTS database_vms_compute_quota_check;
ALTER TABLE database_vms
  DROP COLUMN IF EXISTS compute_quota_unit_seconds,
  DROP COLUMN IF EXISTS held;

RESET lock_timeout;
