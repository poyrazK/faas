-- +goose Up
ALTER TABLE managed_postgres_cutover_preflights
 ADD COLUMN IF NOT EXISTS runtime_drain jsonb,
 ADD COLUMN IF NOT EXISTS runtime_drain_fingerprint text NOT NULL DEFAULT '';

-- Retain an existing definition when replay follows a later migration.
-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint
  WHERE conrelid='managed_postgres_cutover_preflights'::regclass
   AND conname='managed_postgres_preflight_runtime_evidence') THEN
  ALTER TABLE managed_postgres_cutover_preflights
   ADD CONSTRAINT managed_postgres_preflight_runtime_evidence CHECK (
  (runtime_drain IS NULL AND runtime_drain_fingerprint='') OR
  (runtime_drain IS NOT NULL AND jsonb_typeof(runtime_drain)='object' AND runtime_drain_fingerprint ~ '^[0-9a-f]{64}$'));
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint
  WHERE conrelid='managed_postgres_cutover_preflights'::regclass
   AND conname='managed_postgres_preflight_pending_runtime') THEN
  ALTER TABLE managed_postgres_cutover_preflights
   ADD CONSTRAINT managed_postgres_preflight_pending_runtime CHECK (state<>'pending' OR runtime_drain IS NULL);
 END IF;
END;
$migration$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE managed_postgres_cutover_preflights
 DROP CONSTRAINT managed_postgres_preflight_pending_runtime,
 DROP CONSTRAINT managed_postgres_preflight_runtime_evidence,
 DROP COLUMN runtime_drain_fingerprint,
 DROP COLUMN runtime_drain;
