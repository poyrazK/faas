-- migration-compat: expand
-- filename: 20261110213000417_managed_postgres_overage_egress.sql
--
-- ADR-20261110160000 slice 3: database egress past the plan's shared egress
-- allowance is billed on the same managed PostgreSQL overage delivery row as
-- compute and storage, for a subject that elected egress overage. The row
-- records the egress bytes its cumulative charge billed, and a reserved step
-- records the egress it was computed from. The previous release's statements
-- do not name the columns, so its rows keep the defaults; the charge itself
-- stays cumulative in delivered_millicents and target_millicents. The CHECKs
-- are added NOT VALID: new writes are checked at once. Every step is
-- re-runnable.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_overage_deliveries
    ADD COLUMN IF NOT EXISTS billed_egress_bytes bigint NOT NULL DEFAULT 0;
ALTER TABLE managed_postgres_overage_pending
    ADD COLUMN IF NOT EXISTS used_egress_bytes bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS app_egress_bytes bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS egress_allowance_bytes bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS billed_egress_bytes bigint NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
DO $check$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'managed_postgres_overage_deliveries'::regclass
      AND conname = 'managed_postgres_overage_deliveries_billed_egress_check'
  ) THEN
    ALTER TABLE managed_postgres_overage_deliveries
      ADD CONSTRAINT managed_postgres_overage_deliveries_billed_egress_check
      CHECK (billed_egress_bytes >= 0) NOT VALID;
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'managed_postgres_overage_pending'::regclass
      AND conname = 'managed_postgres_overage_pending_egress_check'
  ) THEN
    ALTER TABLE managed_postgres_overage_pending
      ADD CONSTRAINT managed_postgres_overage_pending_egress_check
      CHECK (used_egress_bytes >= 0 AND app_egress_bytes >= 0 AND egress_allowance_bytes >= 0 AND billed_egress_bytes >= 0) NOT VALID;
  END IF;
END
$check$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_overage_pending
    DROP CONSTRAINT IF EXISTS managed_postgres_overage_pending_egress_check;
ALTER TABLE managed_postgres_overage_deliveries
    DROP CONSTRAINT IF EXISTS managed_postgres_overage_deliveries_billed_egress_check;
ALTER TABLE managed_postgres_overage_pending
    DROP COLUMN IF EXISTS billed_egress_bytes,
    DROP COLUMN IF EXISTS egress_allowance_bytes,
    DROP COLUMN IF EXISTS app_egress_bytes,
    DROP COLUMN IF EXISTS used_egress_bytes;
ALTER TABLE managed_postgres_overage_deliveries
    DROP COLUMN IF EXISTS billed_egress_bytes;
-- +goose StatementEnd
