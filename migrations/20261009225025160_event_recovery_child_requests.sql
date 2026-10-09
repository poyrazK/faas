-- +goose Up
-- +goose StatementBegin
ALTER TABLE event_recovery_jobs ADD COLUMN request_id uuid;
-- Restore retained identities after Down preserved their immutable JSON lineage.
UPDATE event_recovery_jobs SET request_id=(selection->>'request_id')::uuid
 WHERE coalesce(selection->>'mode','')='execution' AND coalesce(selection->>'parent_job_id','')<>''
  AND coalesce(selection->>'request_id','')<>'';
ALTER TABLE event_recovery_jobs ADD CONSTRAINT event_recovery_child_request_chk CHECK (
  (request_id IS NULL AND coalesce(selection->>'parent_job_id','')='' AND coalesce(selection->>'request_id','')='')
  OR (request_id IS NOT NULL AND coalesce(selection->>'mode','')='execution'
   AND coalesce(selection->>'parent_job_id','')<>'' AND coalesce(selection->>'request_id','')=request_id::text)
 );
CREATE UNIQUE INDEX event_recovery_child_request_identity_idx ON event_recovery_jobs(account_id,request_id)
 WHERE request_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS event_recovery_child_request_identity_idx;
ALTER TABLE event_recovery_jobs DROP CONSTRAINT IF EXISTS event_recovery_child_request_chk,
 DROP COLUMN IF EXISTS request_id;
-- Keep historical lineage in selection/expected_progress JSON.
-- +goose StatementEnd
