-- migration-compat: expand
-- meterd's billing-window reads select the residency intervals that overlap a
-- recent window: the few still open or ended inside it. The only range index
-- leads with started_at, so each read walked the whole interval history
-- (36,725 rows on prod on 2026-11-01, growing with every wake) once a minute.
-- This index serves "ended_at > window start OR ended_at IS NULL". The
-- previous release never uses it.
--
-- The index is built concurrently (NO TRANSACTION), so
-- instance_billing_intervals keeps taking writes. Re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

CREATE INDEX CONCURRENTLY IF NOT EXISTS instance_billing_intervals_ended_idx
    ON instance_billing_intervals (ended_at);

-- A failed concurrent build can leave an invalid index. Do not record success
-- when IF NOT EXISTS skips one; require explicit cleanup before retrying.
-- +goose StatementBegin
DO $index$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('instance_billing_intervals_ended_idx')
                 AND indisvalid AND indisready) THEN
    RAISE EXCEPTION 'instance_billing_intervals_ended_idx is invalid: DROP INDEX CONCURRENTLY instance_billing_intervals_ended_idx and retry the migration';
  END IF;
END
$index$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS instance_billing_intervals_ended_idx;
RESET lock_timeout;
