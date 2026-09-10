-- +goose Up
-- Persist the Phase-2 handoff start so the migrating-instance watchdog can
-- distinguish an in-flight migration from a wedged one after a schedd restart.
-- The partial index keeps the one-second watchdog query bounded to rows that
-- can actually be candidates for reconciliation.
ALTER TABLE instances
    ADD COLUMN IF NOT EXISTS migration_started_at timestamptz;

UPDATE instances
   SET migration_started_at = now()
 WHERE state = 'migrating'
   AND lease_token IS NOT NULL
   AND migration_started_at IS NULL;

CREATE INDEX IF NOT EXISTS instances_migrating_started_idx
    ON instances (migration_started_at, id)
 WHERE state = 'migrating' AND lease_token IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS instances_migrating_started_idx;
ALTER TABLE instances DROP COLUMN IF EXISTS migration_started_at;
