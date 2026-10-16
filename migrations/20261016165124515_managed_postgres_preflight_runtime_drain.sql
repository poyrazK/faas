-- +goose Up
ALTER TABLE managed_postgres_cutover_preflights
 ADD COLUMN runtime_drain jsonb,
 ADD COLUMN runtime_drain_fingerprint text NOT NULL DEFAULT '',
 ADD CONSTRAINT managed_postgres_preflight_runtime_evidence CHECK (
  (runtime_drain IS NULL AND runtime_drain_fingerprint='') OR
  (runtime_drain IS NOT NULL AND jsonb_typeof(runtime_drain)='object' AND runtime_drain_fingerprint ~ '^[0-9a-f]{64}$')),
 ADD CONSTRAINT managed_postgres_preflight_pending_runtime CHECK (state<>'pending' OR runtime_drain IS NULL);

-- +goose Down
ALTER TABLE managed_postgres_cutover_preflights
 DROP CONSTRAINT managed_postgres_preflight_pending_runtime,
 DROP CONSTRAINT managed_postgres_preflight_runtime_evidence,
 DROP COLUMN runtime_drain_fingerprint,
 DROP COLUMN runtime_drain;
