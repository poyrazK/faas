-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
-- Cancel only reservations that could never own the named destination. The
-- preceding migration guards new writes; keyset batches repair older rows
-- without retaining their locks for the whole scan. Replay is idempotent.
-- +goose StatementBegin
DO $repair$
DECLARE last_id uuid := '00000000-0000-0000-0000-000000000000';
BEGIN
 LOOP
  PERFORM set_config('lock_timeout', '5s', true);
  WITH batch AS (
   SELECT id FROM data_pipeline_setups WHERE id>last_id ORDER BY id LIMIT 1000
  ), repaired AS (
   UPDATE data_pipeline_setups s SET state='cancelled',reason='destination_unavailable',
    revision=revision+1,lease_token='',lease_until=NULL,updated_at=clock_timestamp()
   FROM batch,managed_postgres_databases d
   WHERE s.id=batch.id AND s.database_id=d.id AND s.state IN ('working','blocked')
    AND (s.account_id<>d.account_id OR d.environment_clone_operation_id IS NOT NULL)
  )
  SELECT id INTO last_id FROM batch ORDER BY id DESC LIMIT 1;
  EXIT WHEN last_id IS NULL;
  COMMIT;
 END LOOP;
END $repair$;
-- +goose StatementEnd

-- +goose Down
-- Retain repaired states and revisions; invalid reservations cannot resume.
SELECT 1;
