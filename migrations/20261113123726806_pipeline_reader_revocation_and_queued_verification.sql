-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
-- Each statement commits independently: validate before retiring the old bound.
-- +goose StatementBegin
DO $migration$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='data_pipelines'::regclass AND conname='data_pipelines_read_subjects_v2_check') THEN
  ALTER TABLE data_pipelines ADD CONSTRAINT data_pipelines_read_subjects_v2_check
   CHECK (jsonb_typeof(read_subjects)='array' AND jsonb_array_length(read_subjects) BETWEEN 0 AND 32) NOT VALID;
 END IF;
END $migration$;
-- +goose StatementEnd
ALTER TABLE data_pipelines VALIDATE CONSTRAINT data_pipelines_read_subjects_v2_check;
ALTER TABLE data_pipelines DROP CONSTRAINT IF EXISTS data_pipelines_read_subjects_check;
ALTER TABLE data_pipeline_schema_refreshes ADD COLUMN IF NOT EXISTS read_gate boolean NOT NULL DEFAULT false;

-- Upgrade only requests that have not yet acquired a physical proof identity.
-- Release row locks after each bounded batch; a contended batch can be retried.
-- +goose StatementBegin
DO $migration$
DECLARE changed integer;
BEGIN
 LOOP
  WITH batch AS (
   SELECT id FROM data_pipelines
   WHERE verify_requested AND coalesce(verification_progress->>'ID','')='' AND (verification_progress->>'ManualAttempt')::bigint>0
   ORDER BY id LIMIT 1000 FOR UPDATE
  )
  UPDATE data_pipelines p SET verification_progress=p.verification_progress || jsonb_build_object(
   'ID',encode(sha256(convert_to(p.id::text||':'||p.generation::text||':'||(p.verification_progress->>'ManualAttempt')||':queued','UTF8')),'hex'),
   'State','waiting','Phase','queued') FROM batch WHERE p.id=batch.id;
  GET DIAGNOSTICS changed=ROW_COUNT;
  COMMIT;
  EXIT WHEN changed=0;
 END LOOP;
END $migration$;
-- +goose StatementEnd
RESET lock_timeout;

-- +goose Down
-- Empty reader sets are an intentional access revocation. Never invent a reader
-- or make rollback fail for an account that has already revoked its last reader.
SELECT 1;
