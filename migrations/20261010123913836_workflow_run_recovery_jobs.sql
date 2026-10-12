-- +goose Up
-- +goose StatementBegin
-- The complete feature marker survives later migrations. Reconciliation must
-- preserve retained evidence and must not restore an earlier, narrower constraint.
DO $replay_guard$
BEGIN
 IF to_regclass('event_recovery_items_workflow_run_key') IS NOT NULL AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='event_recovery_items'::regclass AND conname='event_recovery_items_target_check') THEN
  RETURN;
 END IF;
ALTER TABLE event_recovery_items ALTER COLUMN outbox_id DROP NOT NULL;
ALTER TABLE event_recovery_items DROP CONSTRAINT event_recovery_items_subscription_id_check;
ALTER TABLE event_recovery_items ADD CONSTRAINT event_recovery_items_target_check CHECK (
 (outbox_id IS NOT NULL AND subscription_id<>'' AND NOT expected_progress ? 'run_id')
 OR (outbox_id IS NULL AND subscription_id='' AND event_source='' AND event_id='' AND event_type=''
 AND coalesce(expected_progress->>'run_id','') ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
 AND coalesce(jsonb_typeof(expected_progress->'expected_resume_count'),'')='number'
 AND (expected_progress->>'expected_resume_count')::integer BETWEEN 0 AND 16)
);
CREATE UNIQUE INDEX event_recovery_items_workflow_run_key ON event_recovery_items(job_id,(expected_progress->>'run_id')) WHERE outbox_id IS NULL;
ALTER TABLE event_recovery_jobs DROP CONSTRAINT event_recovery_child_request_chk;
ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_child_request_chk CHECK (
 (request_id IS NULL AND coalesce(selection->>'parent_job_id','')='' AND coalesce(selection->>'request_id','')='')
 OR (request_id IS NOT NULL AND coalesce(selection->>'request_id','')=request_id::text AND (
  (coalesce(selection->>'mode','')='execution' AND coalesce(selection->>'parent_job_id','')<>'')
  OR (coalesce(selection->>'mode','')='workflow' AND coalesce(selection->>'parent_job_id','')='' AND coalesce(jsonb_typeof(selection->'workflow_runs'),'')='object')
  OR (coalesce(selection->>'mode','')='routing' AND coalesce(selection->>'parent_job_id','')=''
   AND coalesce(jsonb_typeof(selection->'workflow_routing'),'')='object')
 ))
);
END
$replay_guard$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM event_recovery_jobs WHERE selection ? 'workflow_runs') THEN
  RAISE EXCEPTION 'cannot downgrade while workflow run recovery jobs are retained';
 END IF;
END $$;
DROP INDEX event_recovery_items_workflow_run_key;
ALTER TABLE event_recovery_items DROP CONSTRAINT event_recovery_items_target_check;
ALTER TABLE event_recovery_items ALTER COLUMN outbox_id SET NOT NULL;
ALTER TABLE event_recovery_items ADD CONSTRAINT event_recovery_items_subscription_id_check CHECK(subscription_id<>'');
ALTER TABLE event_recovery_jobs DROP CONSTRAINT event_recovery_child_request_chk;
ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_child_request_chk CHECK (
 (request_id IS NULL AND coalesce(selection->>'parent_job_id','')='' AND coalesce(selection->>'request_id','')='')
 OR (request_id IS NOT NULL AND coalesce(selection->>'request_id','')=request_id::text AND (
  (coalesce(selection->>'mode','')='execution' AND coalesce(selection->>'parent_job_id','')<>'')
  OR (coalesce(selection->>'mode','')='routing' AND coalesce(selection->>'parent_job_id','')=''
   AND coalesce(jsonb_typeof(selection->'workflow_routing'),'')='object')
 ))
);
-- +goose StatementEnd
