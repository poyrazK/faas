-- +goose Up
-- +goose StatementBegin
ALTER TABLE event_recovery_jobs DROP CONSTRAINT event_recovery_child_request_chk;
ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_child_request_chk CHECK (
 (request_id IS NULL AND coalesce(selection->>'parent_job_id','')='' AND coalesce(selection->>'request_id','')='')
 OR (request_id IS NOT NULL AND coalesce(selection->>'request_id','')=request_id::text AND (
  (coalesce(selection->>'mode','')='execution' AND coalesce(selection->>'parent_job_id','')<>'')
  OR (coalesce(selection->>'mode','')='workflow' AND coalesce(jsonb_typeof(selection->'workflow_runs'),'')='object')
  OR (coalesce(selection->>'mode','')='routing' AND coalesce(selection->>'parent_job_id','')=''
   AND coalesce(jsonb_typeof(selection->'workflow_routing'),'')='object')
 ))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM event_recovery_jobs WHERE selection->>'mode'='workflow' AND coalesce(selection->>'parent_job_id','')<>'') THEN
  RAISE EXCEPTION 'cannot downgrade while workflow recovery retry children are retained';
 END IF;
END $$;
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
-- +goose StatementEnd
