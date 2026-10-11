-- +goose Up
-- +goose StatementBegin
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

-- +goose Down
-- +goose StatementBegin
-- Never discard retained idempotency identities to make downgrade succeed.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM event_recovery_jobs WHERE request_id IS NOT NULL AND selection ? 'workflow_routing') THEN
  RAISE EXCEPTION 'cannot downgrade while reviewed workflow recovery jobs are retained';
 END IF;
END $$;
ALTER TABLE event_recovery_jobs DROP CONSTRAINT event_recovery_child_request_chk;
ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_child_request_chk CHECK (
 (request_id IS NULL AND coalesce(selection->>'parent_job_id','')='' AND coalesce(selection->>'request_id','')='')
 OR (request_id IS NOT NULL AND coalesce(selection->>'mode','')='execution'
  AND coalesce(selection->>'parent_job_id','')<>'' AND coalesce(selection->>'request_id','')=request_id::text)
);
-- +goose StatementEnd
