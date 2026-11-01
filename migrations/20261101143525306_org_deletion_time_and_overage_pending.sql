-- filename: 20261101143525306_org_deletion_time_and_overage_pending.sql

-- ADR-20261101143527 (billing audit round 4).
--
-- 1. orgs.deletion_requested_at records when an organization was marked for
--    deletion, so meterd keeps delivering the usage it ran before then, as it
--    already does for accounts. A trigger stamps it whenever deleted_pending
--    turns on, on every path and for the previous release's writes too.
--    Organizations already pending deletion take their last update time.
-- 2. managed_postgres_overage_pending holds a managed PostgreSQL overage step
--    apid reserved before sending it to the billing provider. A step that was
--    sent but not recorded is sent again with the same target, which the
--    provider deduplicates, instead of a new one charged on top of it.
--    apid is the only writer. A new table; the previous release never reads it.
-- migration-compat: expand
--
-- The CHECK is added NOT VALID and validated in its own statement; a VALIDATE
-- takes SHARE UPDATE EXCLUSIVE, so orgs keep being written during the scan.
-- Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE orgs ADD COLUMN IF NOT EXISTS deletion_requested_at timestamptz;

-- +goose StatementBegin
DO $deletion$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'orgs'::regclass AND conname = 'orgs_deletion_requested_at_chk') THEN
    ALTER TABLE orgs ADD CONSTRAINT orgs_deletion_requested_at_chk
      CHECK (deletion_requested_at IS NULL OR (deleted_pending AND isfinite(deletion_requested_at))) NOT VALID;
  END IF;
END
$deletion$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION stamp_org_deletion_requested_at() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NOT NEW.deleted_pending THEN
  NEW.deletion_requested_at := NULL;
 ELSIF NEW.deletion_requested_at IS NULL OR (TG_OP = 'UPDATE' AND NOT OLD.deleted_pending) THEN
  NEW.deletion_requested_at := now();
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS orgs_deletion_requested_at ON orgs;
CREATE TRIGGER orgs_deletion_requested_at BEFORE INSERT OR UPDATE OF deleted_pending, deletion_requested_at ON orgs
    FOR EACH ROW EXECUTE FUNCTION stamp_org_deletion_requested_at();

UPDATE orgs SET deletion_requested_at = updated_at WHERE deleted_pending AND deletion_requested_at IS NULL;

ALTER TABLE orgs VALIDATE CONSTRAINT orgs_deletion_requested_at_chk;

COMMENT ON COLUMN orgs.deletion_requested_at IS
    'When the organization was marked for deletion; meterd delivers usage from before it (ADR-20261101143527).';

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS managed_postgres_overage_pending (
    provider text NOT NULL CHECK (provider ~ '^[a-z][a-z0-9_]{0,31}$'),
    subject_id uuid NOT NULL REFERENCES resource_namespaces(id) ON DELETE CASCADE,
    period_start timestamptz NOT NULL
        CHECK (period_start = (date_trunc('month', period_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')),
    live boolean NOT NULL,
    step_at timestamptz NOT NULL CHECK (isfinite(step_at) AND step_at >= period_start),
    used_unit_seconds bigint NOT NULL CHECK (used_unit_seconds >= 0),
    allowance_unit_seconds bigint NOT NULL CHECK (allowance_unit_seconds >= 0),
    billed_unit_seconds bigint NOT NULL CHECK (billed_unit_seconds >= 0),
    from_millicents bigint NOT NULL CHECK (from_millicents >= 0),
    target_millicents bigint NOT NULL,
    CONSTRAINT managed_postgres_overage_pending_target_check CHECK (target_millicents > from_millicents),
    PRIMARY KEY (provider, subject_id, period_start)
);
-- +goose StatementEnd

COMMENT ON TABLE managed_postgres_overage_pending IS
    'A managed PostgreSQL overage step reserved before it is sent to the billing provider; written only by apid (ADR-20261101143527).';

-- +goose Down
SET lock_timeout = '5s';
DROP TABLE IF EXISTS managed_postgres_overage_pending;
DROP TRIGGER IF EXISTS orgs_deletion_requested_at ON orgs;
DROP FUNCTION IF EXISTS stamp_org_deletion_requested_at();
ALTER TABLE orgs DROP CONSTRAINT IF EXISTS orgs_deletion_requested_at_chk;
ALTER TABLE orgs DROP COLUMN IF EXISTS deletion_requested_at;
