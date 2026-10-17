-- name: PipelineAccountLock :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(account_id)::text,51731));

-- name: PipelineConnectionCount :one
SELECT count(*) FROM data_pipeline_connections WHERE account_id=sqlc.arg(account_id)::text::uuid;

-- name: PipelineInsertConnection :one
INSERT INTO data_pipeline_connections(id,account_id,name,ciphertext,identity)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(name),sqlc.arg(ciphertext),sqlc.arg(identity)) RETURNING *;

-- name: PipelineConnections :many
SELECT * FROM data_pipeline_connections WHERE account_id=sqlc.arg(account_id)::text::uuid ORDER BY name LIMIT sqlc.arg(page_limit);

-- name: PipelineConnection :one
SELECT * FROM data_pipeline_connections WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid;

-- name: PipelineRotateConnection :one
UPDATE data_pipeline_connections SET ciphertext=sqlc.arg(ciphertext),revision=revision+1
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision) RETURNING *;

-- name: PipelineDeleteConnection :execrows
DELETE FROM data_pipeline_connections c WHERE c.account_id=sqlc.arg(account_id)::text::uuid AND c.id=sqlc.arg(id)::text::uuid
 AND NOT EXISTS(SELECT 1 FROM data_pipelines p WHERE p.source_connection_id=c.id AND p.state<>'deleted')
 AND NOT EXISTS(SELECT 1 FROM data_pipeline_setups s WHERE s.source_connection_id=c.id AND s.state IN ('working','blocked'));

-- name: PipelineCount :one
SELECT count(*) FROM data_pipelines WHERE account_id=sqlc.arg(account_id)::text::uuid AND state<>'deleted';

-- name: PipelineInsert :one
INSERT INTO data_pipelines(id,account_id,name,source_connection_id,database_id,tables,read_subjects)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(name),sqlc.arg(source_connection_id)::text::uuid,sqlc.arg(database_id)::text::uuid,sqlc.arg(tables),sqlc.arg(read_subjects)) RETURNING *;

-- name: PipelinesList :many
SELECT * FROM data_pipelines WHERE account_id=sqlc.arg(account_id)::text::uuid ORDER BY (state='deleted'),created_at DESC,id LIMIT sqlc.arg(page_limit);

-- name: PipelineGet :one
SELECT * FROM data_pipelines WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid;

-- name: PipelineGetByName :one
SELECT * FROM data_pipelines WHERE account_id=sqlc.arg(account_id)::text::uuid AND name=sqlc.arg(name);

-- name: PipelineControl :one
UPDATE data_pipelines SET
 verification_progress=CASE WHEN sqlc.arg(generation_delta)::bigint>0 OR sqlc.arg(verify_requested)::boolean THEN '{}'::jsonb ELSE verification_progress END,
 schema_change=CASE WHEN sqlc.arg(generation_delta)::bigint>0 THEN '{}'::jsonb ELSE schema_change END,
 schema_approval=CASE WHEN sqlc.arg(generation_delta)::bigint>0 THEN '' ELSE schema_approval END,
 desired_state=sqlc.arg(desired_state),state=sqlc.arg(state),generation=generation+sqlc.arg(generation_delta)::bigint,
 applied_through_at=CASE WHEN sqlc.arg(generation_delta)::bigint>0 THEN NULL ELSE applied_through_at END,
 initialized=CASE WHEN sqlc.arg(generation_delta)::bigint>0 THEN false ELSE initialized END,
 revision=revision+1,verify_requested=sqlc.arg(verify_requested),verification=CASE WHEN sqlc.arg(generation_delta)::bigint>0 THEN 'unknown' WHEN sqlc.arg(verify_requested) THEN 'pending' ELSE verification END,
 verified_at=CASE WHEN sqlc.arg(verify_requested) OR sqlc.arg(generation_delta)::bigint>0 THEN NULL ELSE verified_at END,
 retry_at=clock_timestamp()
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision) AND state<>'deleted'
 AND (coalesce(reader_change->>'State','')<>'approved' OR sqlc.arg(generation_delta)::bigint=0)
RETURNING *;

-- name: PipelineClaim :one
WITH candidate AS (
 SELECT id FROM data_pipelines WHERE state<>'deleted' AND retry_at<=clock_timestamp()
 AND (lease_until IS NULL OR lease_until<=clock_timestamp())
 ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1
) UPDATE data_pipelines p SET lease_token=sqlc.arg(lease_token),lease_until=clock_timestamp()+interval '60 seconds'
 FROM candidate c WHERE p.id=c.id RETURNING p.*;

-- name: PipelineFinish :execrows
UPDATE data_pipelines SET
 read_subjects=CASE WHEN reader_change->>'State'='approved' AND sqlc.narg(reader_change)::jsonb->>'State'='applied' AND reader_change->>'ID'=sqlc.narg(reader_change)::jsonb->>'ID' THEN reader_change->'After' ELSE read_subjects END,
 reader_change=CASE WHEN reader_change->>'State'='approved' AND sqlc.narg(reader_change)::jsonb->>'State'='applied' AND reader_change->>'ID'=sqlc.narg(reader_change)::jsonb->>'ID' THEN sqlc.narg(reader_change)::jsonb ELSE reader_change END,
 verification_progress=coalesce(sqlc.narg(verification_progress)::jsonb,verification_progress),schema_change=coalesce(sqlc.narg(schema_change)::jsonb,schema_change),observed_revision=revision,initialized=initialized OR sqlc.arg(initialized),state=sqlc.arg(state),reason=sqlc.arg(reason),tables_ready=sqlc.arg(tables_ready),
 tables=CASE WHEN sqlc.narg(schema_change)::jsonb->'Cancellation'->>'State'='complete'
 AND schema_change->'Cancellation'->>'State'='approved' AND schema_change->'Cancellation'->>'ID'=sqlc.narg(schema_change)::jsonb->'Cancellation'->>'ID'
 THEN sqlc.narg(schema_change)::jsonb->'RequestedTables' ELSE tables END,
 schema_approval=CASE WHEN sqlc.narg(schema_change)::jsonb->'Cancellation'->>'State'='complete'
 AND schema_change->'Cancellation'->>'State'='approved' AND schema_change->'Cancellation'->>'ID'=sqlc.narg(schema_change)::jsonb->'Cancellation'->>'ID'
 THEN sqlc.narg(schema_change)::jsonb->>'ID'
 WHEN sqlc.narg(schema_change)::jsonb->'Recovery'->>'Kind'='live' AND sqlc.narg(schema_change)::jsonb->'Recovery'->>'State'='detected'
 AND sqlc.arg(reason)::text='live_table_mismatch' THEN sqlc.narg(schema_change)::jsonb->>'ID'
 WHEN sqlc.narg(schema_change)::jsonb->>'State'='withdrawn' THEN '' ELSE schema_approval END,
 applied_through_at=coalesce(sqlc.narg(applied_through_at),applied_through_at),
 retained_wal_bytes=sqlc.narg(retained_wal_bytes),observed_at=clock_timestamp(),
 verification=sqlc.arg(verification),verified_at=sqlc.narg(verified_at),verify_requested=(sqlc.arg(verification)='pending'),
 lease_token='',lease_until=NULL,retry_at=clock_timestamp()+interval '15 seconds'
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND revision=sqlc.arg(revision) AND lease_token=sqlc.arg(lease_token) AND lease_until>clock_timestamp();

-- name: PipelineRelease :exec
UPDATE data_pipelines SET lease_token='',lease_until=NULL WHERE id=sqlc.arg(id)::text::uuid AND lease_token=sqlc.arg(lease_token);

-- name: PipelineTargetInUse :one
SELECT EXISTS(SELECT 1 FROM data_pipelines WHERE account_id=sqlc.arg(account_id)::text::uuid AND database_id=sqlc.arg(database_id)::text::uuid AND state<>'deleted');

-- name: PipelineAdmitCapacity :one
SELECT postgres_capacity_admit(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(revision)::bigint,sqlc.arg(lease_token)::text)::boolean AS admitted;

-- name: PipelinePruneHistory :exec
DELETE FROM data_pipelines WHERE id IN (SELECT id FROM data_pipelines WHERE account_id=sqlc.arg(account_id)::text::uuid AND state='deleted' ORDER BY created_at DESC,id OFFSET sqlc.arg(history_offset) LIMIT sqlc.arg(page_limit));

-- name: PipelineCheckConnectionLock :one
SELECT * FROM data_pipeline_connections WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid FOR UPDATE;

-- name: PipelineCheckGet :one
SELECT ch.*,c.revision AS current_revision FROM data_connection_checks ch
JOIN data_pipeline_connections c ON c.id=ch.connection_id AND c.account_id=ch.account_id
WHERE ch.account_id=sqlc.arg(account_id)::text::uuid AND ch.connection_id=sqlc.arg(connection_id)::text::uuid;

-- name: PipelineCheckForCreate :one
SELECT ch.*,c.revision AS current_revision FROM data_connection_checks ch
JOIN data_pipeline_connections c ON c.id=ch.connection_id AND c.account_id=ch.account_id
WHERE ch.account_id=sqlc.arg(account_id)::text::uuid AND ch.connection_id=sqlc.arg(connection_id)::text::uuid
FOR SHARE OF c,ch;

-- name: PipelineCheckRequest :one
INSERT INTO data_connection_checks(connection_id,account_id,id,connection_revision,database_id)
VALUES(sqlc.arg(connection_id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(id)::text::uuid,sqlc.arg(connection_revision),sqlc.arg(database_id))
ON CONFLICT(connection_id) DO UPDATE SET id=excluded.id,connection_revision=excluded.connection_revision,database_id=excluded.database_id,
 state='pending',result='{}',lease_token='',lease_until=NULL,created_at=clock_timestamp(),observed_at=NULL
RETURNING *;

-- name: PipelineCheckClaim :one
WITH candidate AS (
 SELECT ch.connection_id FROM data_connection_checks ch
 JOIN data_pipeline_connections c ON c.id=ch.connection_id AND c.account_id=ch.account_id AND c.revision=ch.connection_revision
 WHERE ch.state IN ('pending','running') AND (ch.lease_until IS NULL OR ch.lease_until<=clock_timestamp())
 ORDER BY ch.created_at,ch.connection_id FOR UPDATE OF ch SKIP LOCKED LIMIT 1
) UPDATE data_connection_checks ch SET state='running',lease_token=sqlc.arg(lease_token),lease_until=clock_timestamp()+interval '60 seconds'
FROM candidate WHERE ch.connection_id=candidate.connection_id RETURNING ch.*;

-- name: PipelineCheckFinish :execrows
UPDATE data_connection_checks ch SET state=sqlc.arg(state),result=sqlc.arg(result),observed_at=clock_timestamp(),lease_token='',lease_until=NULL
FROM data_pipeline_connections c
WHERE ch.connection_id=c.id AND ch.account_id=c.account_id AND c.revision=ch.connection_revision
 AND ch.account_id=sqlc.arg(account_id)::text::uuid AND ch.id=sqlc.arg(id)::text::uuid
 AND ch.connection_revision=sqlc.arg(connection_revision) AND ch.lease_token=sqlc.arg(lease_token) AND ch.lease_until>clock_timestamp();

-- name: PipelineSetupByName :one
SELECT * FROM data_pipeline_setups WHERE account_id=sqlc.arg(account_id)::text::uuid AND name=sqlc.arg(name);

-- name: PipelineSetupGet :one
SELECT * FROM data_pipeline_setups WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid;

-- name: PipelineSetupList :many
SELECT * FROM data_pipeline_setups WHERE account_id=sqlc.arg(account_id)::text::uuid ORDER BY created_at DESC,id LIMIT sqlc.arg(page_limit);

-- name: PipelineSetupCount :one
SELECT count(*) FROM data_pipeline_setups WHERE account_id=sqlc.arg(account_id)::text::uuid AND state IN ('working','blocked');

-- name: PipelineSetupPrune :exec
DELETE FROM data_pipeline_setups WHERE id IN (SELECT id FROM data_pipeline_setups WHERE account_id=sqlc.arg(account_id)::text::uuid AND state='cancelled' AND phase='checking_source' AND pipeline_id='' ORDER BY created_at DESC,id OFFSET sqlc.arg(history_offset) LIMIT sqlc.arg(page_limit));

-- name: PipelineSetupInsert :one
INSERT INTO data_pipeline_setups(id,account_id,source_connection_id,name,request,mirror_plan,data_api_plan,database_id,mirror_name)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(connection_id)::text::uuid,sqlc.arg(name),sqlc.arg(request),sqlc.arg(mirror_plan),sqlc.arg(data_api_plan),sqlc.arg(database_id)::text::uuid,sqlc.arg(mirror_name)) RETURNING *;

-- name: PipelineSetupControl :one
UPDATE data_pipeline_setups SET state=sqlc.arg(state),revision=revision+1,reason='',retry_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision)
 AND (state='blocked' OR (sqlc.arg(state)::text='cancelled' AND state='working')) RETURNING *;

-- name: PipelineSetupClaim :one
WITH candidate AS (
 SELECT id FROM data_pipeline_setups WHERE state='working' AND retry_at<=clock_timestamp()
 AND (lease_until IS NULL OR lease_until<=clock_timestamp()) ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1
) UPDATE data_pipeline_setups s SET lease_token=sqlc.arg(lease_token),lease_until=clock_timestamp()+interval '60 seconds'
FROM candidate c WHERE s.id=c.id RETURNING s.*;

-- name: PipelineSetupFinish :execrows
UPDATE data_pipeline_setups SET state=sqlc.arg(state),phase=sqlc.arg(phase),reason=sqlc.arg(reason),pipeline_id=sqlc.arg(pipeline_id),data_api_plan=sqlc.arg(data_api_plan),
 lease_token='',lease_until=NULL,updated_at=clock_timestamp(),retry_at=clock_timestamp()+interval '2 seconds'
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid AND revision=sqlc.arg(revision)
 AND state='working' AND lease_token=sqlc.arg(lease_token) AND lease_until>clock_timestamp();

-- name: PipelineSetupRelease :exec
UPDATE data_pipeline_setups SET lease_token='',lease_until=NULL WHERE id=sqlc.arg(id)::text::uuid AND lease_token=sqlc.arg(lease_token);

-- name: PipelineSetupTotalCount :one
SELECT count(*) FROM data_pipeline_setups WHERE account_id=sqlc.arg(account_id)::text::uuid;

-- name: PipelineSetupUnstartedCount :one
SELECT count(*) FROM data_pipeline_setups WHERE account_id=sqlc.arg(account_id)::text::uuid AND state IN ('working','blocked') AND pipeline_id='';

-- name: PipelineSetupOtherReservations :one
SELECT count(*) FROM data_pipeline_setups WHERE account_id=sqlc.arg(account_id)::text::uuid AND state IN ('working','blocked') AND pipeline_id=''
 AND NOT (name=sqlc.arg(name) AND database_id=sqlc.arg(database_id)::text::uuid AND source_connection_id=sqlc.arg(connection_id)::text::uuid);

-- name: PipelineApproveSchema :execrows
UPDATE data_pipelines SET schema_approval=sqlc.arg(change_id),revision=revision+1,retry_at=clock_timestamp(),
	 tables=CASE WHEN jsonb_array_length(coalesce(schema_change->'AddedTables','[]'::jsonb))>0 THEN schema_change->'RequestedTables' ELSE tables END,
	 verification=CASE WHEN jsonb_array_length(coalesce(schema_change->'AddedTables','[]'::jsonb))>0 THEN 'unknown' ELSE verification END,
	 verified_at=CASE WHEN jsonb_array_length(coalesce(schema_change->'AddedTables','[]'::jsonb))>0 THEN NULL ELSE verified_at END,
 schema_change=CASE WHEN sqlc.arg(retry)::boolean THEN jsonb_set(schema_change,'{Retry}',to_jsonb(coalesce((schema_change->>'Retry')::integer,0)+1)) ELSE schema_change END
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid
 AND revision=sqlc.arg(revision) AND desired_state='running'
 AND (state='blocked' OR (state='live' AND reason='table_addition_pending' AND jsonb_array_length(coalesce(schema_change->'AddedTables','[]'::jsonb))>0))
 AND schema_change->>'ID'=sqlc.arg(change_id) AND (schema_change->>'Supported')::boolean
 AND coalesce(schema_change->'Cancellation'->>'State','') NOT IN ('requested','pending','approved')
 AND (coalesce(schema_change->'Recovery'->>'State','') NOT IN ('requested','pending','approved')
 OR schema_change->'Recovery'->>'Stage'='verifying')
 AND (coalesce(schema_change->'Recovery'->>'Kind','')<>'live' OR (sqlc.arg(retry)::boolean AND schema_change->'Recovery'->>'Stage'='verifying'))
 AND coalesce(reader_change->>'State','')<>'approved'
 AND observed_revision=revision AND observed_at>clock_timestamp()-(sqlc.arg(max_age_seconds)::bigint*interval '1 second')
 AND ((NOT sqlc.arg(retry)::boolean AND schema_change->>'State'='pending')
 OR (sqlc.arg(retry)::boolean AND schema_approval=sqlc.arg(change_id) AND schema_change->>'State' IN ('verifying','refreshing') AND coalesce((schema_change->>'Retry')::integer,0)<sqlc.arg(max_retries)::integer));

-- name: PipelineRequestTables :execrows
UPDATE data_pipelines SET schema_change=sqlc.arg(plan)::jsonb,schema_approval='',verification_progress='{}',
 revision=revision+1,retry_at=clock_timestamp()
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision)
 AND state='live' AND desired_state='running' AND NOT verify_requested
 AND coalesce(schema_change->>'State','complete') IN ('complete','withdrawn')
 AND coalesce(verification_progress->>'State','') NOT IN ('running','waiting')
 AND coalesce(reader_change->>'State','')<>'approved'
 AND observed_revision=revision AND observed_at>clock_timestamp()-(sqlc.arg(max_age_seconds)::bigint*interval '1 second');

-- name: PipelineCancelTables :execrows
UPDATE data_pipelines SET schema_change='{}',schema_approval='',reason='',revision=revision+1,retry_at=clock_timestamp()
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision)
 AND schema_approval='' AND jsonb_array_length(coalesce(schema_change->'AddedTables','[]'::jsonb))>0;

-- name: PipelineSchemaRefreshLock :one
SELECT id FROM data_pipelines WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid
 AND revision=sqlc.arg(revision) AND lease_token=sqlc.arg(lease_token) AND lease_until>clock_timestamp()
 AND desired_state='running' AND
 ((schema_approval=sqlc.arg(change_id) AND schema_change->>'ID'=sqlc.arg(change_id)
 AND coalesce(schema_change->'Cancellation'->>'State','') NOT IN ('requested','pending','approved'))
 OR (schema_approval=schema_change->>'ID' AND schema_change->'Cancellation'->>'ID'=sqlc.arg(change_id)
 AND schema_change->'Cancellation'->>'State'='approved')) FOR UPDATE;

-- name: PipelineSchemaRefreshPrune :exec
DELETE FROM data_pipeline_schema_refreshes WHERE pipeline_id=sqlc.arg(pipeline_id)::text::uuid AND change_id<>sqlc.arg(change_id);

-- name: PipelineSchemaRefreshInsert :execrows
INSERT INTO data_pipeline_schema_refreshes(pipeline_id,change_id,attempt,app_id,wake_id)
VALUES(sqlc.arg(pipeline_id)::text::uuid,sqlc.arg(change_id),sqlc.arg(attempt),sqlc.arg(app_id)::text::uuid,sqlc.arg(wake_id)::text::uuid)
ON CONFLICT DO NOTHING;

-- name: PipelineSchemaApps :many
SELECT DISTINCT a.id,a.slug,a.status FROM managed_postgres_bindings b JOIN apps a ON a.id=b.app_id AND a.account_id=b.account_id
WHERE b.account_id=sqlc.arg(account_id)::text::uuid AND b.database_id=sqlc.arg(database_id)::text::uuid AND b.state<>'deleted' AND a.status<>'deleted'
ORDER BY a.id LIMIT sqlc.arg(page_limit);

-- name: PipelineSchemaRefreshStamp :exec
INSERT INTO app_runtime_config_changes(app_id,changed_at) VALUES(sqlc.arg(app_id)::text::uuid,clock_timestamp())
ON CONFLICT(app_id) DO UPDATE SET changed_at=excluded.changed_at;

-- name: PipelineSchemaRefreshSnapshots :exec
UPDATE snapshots SET stale=true WHERE NOT stale AND deployment_id IN (SELECT id FROM deployments WHERE app_id=sqlc.arg(app_id)::text::uuid);

-- name: PipelineCancelVerification :execrows
UPDATE data_pipelines SET revision=revision+1,retry_at=clock_timestamp(),verify_requested=false,
 verification=CASE WHEN verify_requested THEN 'inconclusive' ELSE verification END,
 verification_progress=jsonb_set(verification_progress,'{State}','"cancelled"')
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid
 AND revision=sqlc.arg(revision) AND desired_state='running'
 AND verification_progress->>'ID'=sqlc.arg(run_id)::text
 AND verification_progress->>'State' IN ('running','waiting');

-- name: PipelineRequestTableRecovery :execrows
UPDATE data_pipelines SET revision=revision+1,retry_at=clock_timestamp(),
 schema_change=jsonb_set(schema_change,'{Recovery}',CASE WHEN sqlc.arg(kind)::text='live'
 THEN (schema_change->'Recovery')||jsonb_build_object('State','requested','ID','','Stage','quarantined')
 ELSE jsonb_build_object('Table',sqlc.arg(table_name)::text,'Kind',sqlc.arg(kind)::text,'State','requested') END)
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision)
 AND desired_state='running' AND state='blocked'
 AND schema_approval=schema_change->>'ID' AND schema_change->>'State'<>'complete' AND (schema_change->>'Supported')::boolean
 AND (schema_change->'AddedTables' ? sqlc.arg(table_name)::text OR
 (sqlc.arg(kind)::text='live' AND schema_change->'Recovery'->>'Kind'='live' AND tables=schema_change->'RequestedTables' AND tables ? sqlc.arg(table_name)::text AND schema_change->'Recovery'->>'Table'=sqlc.arg(table_name)::text))
 AND coalesce(schema_change->'Cancellation'->>'State','') NOT IN ('requested','pending','approved')
 AND (coalesce(schema_change->'Recovery'->>'State','') NOT IN ('requested','pending','approved') OR schema_change->'Recovery'->>'Stage'='verifying')
 AND ((sqlc.arg(kind)::text='live' AND schema_change->'Recovery'->>'Kind'='live' AND reason IN ('live_table_mismatch','replica_mismatch')
 AND verification_progress->>'State'='mismatch' AND verification_progress->>'Table'=sqlc.arg(table_name)::text)
 OR (sqlc.arg(kind)::text='' AND coalesce(schema_change->'Recovery'->>'Kind','')<>'live' AND reason='replica_mismatch' AND verification_progress->>'State'='mismatch' AND verification_progress->>'Table'=sqlc.arg(table_name)::text)
 OR (sqlc.arg(kind)::text='copy' AND reason IN ('table_copy_stalled','table_addition_tracking_lost')
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(coalesce(schema_change->'Copies','[]')) c WHERE c->>'Table'=sqlc.arg(table_name)::text
 AND coalesce(c->>'NativeState','')<>'r' AND (coalesce((c->>'Stalled')::boolean,false) OR c->>'Phase'='missing'))
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(coalesce(schema_change->'Copies','[]')) other_copy WHERE other_copy->>'Table'<>sqlc.arg(table_name)::text AND coalesce(other_copy->>'NativeState','')='')))
 AND coalesce((schema_change->>'Retry')::integer,0)<sqlc.arg(max_retries)::integer
 AND observed_revision=revision AND observed_at>clock_timestamp()-(sqlc.arg(max_age_seconds)::bigint*interval '1 second');

-- name: PipelineApproveTableRecovery :execrows
UPDATE data_pipelines SET revision=revision+1,retry_at=clock_timestamp(),verification_progress='{}',
 schema_change=jsonb_set(jsonb_set(schema_change,'{Recovery,State}','"approved"'),'{Retry}',to_jsonb((schema_change->'Recovery'->>'Attempt')::integer))
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision)
 AND desired_state='running' AND state='blocked' AND reason='table_recovery_pending'
 AND schema_approval=schema_change->>'ID' AND schema_change->>'State'<>'complete' AND (schema_change->>'Supported')::boolean
 AND (schema_change->'AddedTables' ? (schema_change->'Recovery'->>'Table') OR
 (schema_change->'Recovery'->>'Kind'='live' AND tables=schema_change->'RequestedTables' AND tables ? (schema_change->'Recovery'->>'Table')))
 AND schema_change->'Recovery'->>'ID'=sqlc.arg(recovery_id)::text AND schema_change->'Recovery'->>'State'='pending'
 AND coalesce(schema_change->'Cancellation'->>'State','') NOT IN ('requested','pending','approved')
 AND (schema_change->'Recovery'->>'Attempt')::integer=coalesce((schema_change->>'Retry')::integer,0)+1
 AND coalesce((schema_change->>'Retry')::integer,0)<sqlc.arg(max_retries)::integer
 AND observed_revision=revision AND observed_at>clock_timestamp()-(sqlc.arg(max_age_seconds)::bigint*interval '1 second');

-- name: PipelineRequestTableCancellation :execrows
UPDATE data_pipelines SET revision=revision+1,retry_at=clock_timestamp(),
 schema_change=jsonb_set(schema_change,'{Cancellation}',jsonb_build_object('Table',sqlc.arg(table_name)::text,'RequestID',sqlc.arg(request_id)::text,'State','requested','Retention','retain_private'))
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision)
 AND desired_state='running' AND state IN ('live','copying','blocked')
 AND schema_approval=schema_change->>'ID' AND schema_change->>'State'<>'complete'
 AND jsonb_array_length(coalesce(schema_change->'Before','[]'))>0 AND schema_change->'AddedTables' ? sqlc.arg(table_name)::text
 AND coalesce(schema_change->'Cancellation'->>'State','')<>'approved'
 AND (coalesce(schema_change->'Recovery'->>'State','')<>'approved' OR schema_change->'Recovery'->>'Stage'='verifying' OR schema_change->'Recovery'->>'Table'=sqlc.arg(table_name)::text)
 AND observed_revision=revision AND observed_at>clock_timestamp()-(sqlc.arg(max_age_seconds)::bigint*interval '1 second');

-- name: PipelineApproveTableCancellation :execrows
UPDATE data_pipelines SET revision=revision+1,retry_at=clock_timestamp(),
 schema_change=jsonb_set(schema_change,'{Cancellation,State}','"approved"')
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision)
 AND desired_state='running' AND state='blocked' AND reason='table_cancellation_pending'
 AND schema_approval=schema_change->>'ID' AND schema_change->>'State'<>'complete'
 AND schema_change->'AddedTables' ? (schema_change->'Cancellation'->>'Table')
 AND schema_change->'Cancellation'->>'ID'=sqlc.arg(cancellation_id)::text AND schema_change->'Cancellation'->>'State'='pending'
 AND schema_change->'Cancellation'->>'Retention'='retain_private'
 AND observed_revision=revision AND observed_at>clock_timestamp()-(sqlc.arg(max_age_seconds)::bigint*interval '1 second');

-- name: PipelineDiscardTableCancellation :execrows
UPDATE data_pipelines SET
 schema_change=CASE WHEN schema_change->'Cancellation'->>'Kind'='live' THEN '{}'::jsonb ELSE schema_change-'Cancellation' END,
 schema_approval=CASE WHEN schema_change->'Cancellation'->>'Kind'='live' THEN '' ELSE schema_approval END,
 state=CASE WHEN schema_change->'Cancellation'->>'Kind'='live' THEN 'live' ELSE state END,
 reason=CASE WHEN schema_change->'Cancellation'->>'Kind'='live' THEN '' ELSE reason END,
 revision=revision+1,retry_at=clock_timestamp()
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision)
 AND desired_state='running' AND state IN ('live','copying','blocked')
 AND schema_change->'Cancellation'->>'State' IN ('requested','pending');

-- name: PipelineRequestTableRemoval :execrows
UPDATE data_pipelines SET schema_change=sqlc.arg(plan)::jsonb,revision=revision+1,retry_at=clock_timestamp()
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision)
 AND state='live' AND desired_state='running' AND initialized AND NOT verify_requested
 AND jsonb_array_length(tables)>1 AND tables ? sqlc.arg(table_name)::text
 AND coalesce(schema_change->>'State','complete') IN ('complete','withdrawn')
 AND coalesce(schema_change->'Cancellation'->>'State','') NOT IN ('requested','pending','approved')
 AND coalesce(verification_progress->>'State','') NOT IN ('running','waiting')
 AND observed_revision=revision AND observed_at>clock_timestamp()-(sqlc.arg(max_age_seconds)::bigint*interval '1 second');

-- name: PipelineApproveTableRemoval :execrows
UPDATE data_pipelines SET schema_approval=schema_change->>'ID',revision=revision+1,retry_at=clock_timestamp(),
 verification_progress='{}',verification='unknown',verified_at=NULL,
 schema_change=jsonb_set(schema_change,'{Cancellation,State}','"approved"')
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision)
 AND desired_state='running' AND initialized AND state='blocked' AND reason='table_removal_pending'
 AND schema_change->>'State'='removal_pending' AND (schema_change->>'Supported')::boolean
 AND schema_change->'Cancellation'->>'Kind'='live' AND schema_change->'Cancellation'->>'State'='pending'
 AND schema_change->'Cancellation'->>'ID'=sqlc.arg(cancellation_id)::text AND schema_change->'Cancellation'->>'Retention'='retain_private'
 AND jsonb_array_length(tables)>1 AND tables=schema_change->'RequestedTables' AND tables ? (schema_change->'Cancellation'->>'Table')
 AND observed_revision=revision AND observed_at>clock_timestamp()-(sqlc.arg(max_age_seconds)::bigint*interval '1 second');

-- name: PipelineReaders :execrows
UPDATE data_pipelines SET reader_change=sqlc.arg(reader_change)::jsonb,revision=revision+1,retry_at=clock_timestamp()
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision) AND initialized AND desired_state<>'deleted'
 AND coalesce(reader_change->>'State','')<>'approved'
 AND (state<>'resync_required' OR sqlc.arg(reader_change)::jsonb='{}')
 AND (coalesce(sqlc.arg(reader_change)::jsonb->>'State','') NOT IN ('preview','approved') OR
 (tables=sqlc.arg(reader_change)::jsonb->'Tables' AND read_subjects=sqlc.arg(reader_change)::jsonb->'Before' AND generation=(sqlc.arg(reader_change)::jsonb->>'Generation')::bigint));

-- name: PipelineFinishReaders :execrows
UPDATE data_pipelines SET read_subjects=reader_change->'After',reader_change=jsonb_set(reader_change,'{State}','"applied"'),
 lease_token='',lease_until=NULL,retry_at=clock_timestamp()
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision)
 AND lease_token=sqlc.arg(lease_token) AND lease_until>clock_timestamp()
 AND reader_change->>'State'='approved' AND reader_change->>'ID'=sqlc.arg(change_id)::text;

-- name: PipelineAlertGet :one
SELECT a.* FROM data_pipeline_alerts a JOIN data_pipelines p ON p.id=a.pipeline_id AND p.account_id=a.account_id
WHERE a.account_id=sqlc.arg(account_id)::text::uuid AND a.pipeline_id=sqlc.arg(pipeline_id)::text::uuid;

-- name: PipelineAlertConfigure :one
INSERT INTO data_pipeline_alerts(pipeline_id,account_id,enabled,ciphertext)
SELECT id,account_id,sqlc.arg(enabled),sqlc.arg(ciphertext) FROM data_pipelines
WHERE id=sqlc.arg(pipeline_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid AND state<>'deleted' AND desired_state<>'deleted' AND (sqlc.arg(expected_revision)::bigint=0 OR EXISTS(SELECT 1 FROM data_pipeline_alerts a WHERE a.pipeline_id=data_pipelines.id AND a.revision=sqlc.arg(expected_revision)::bigint))
ON CONFLICT(pipeline_id) DO UPDATE SET revision=data_pipeline_alerts.revision+1, enabled=sqlc.arg(enabled),ciphertext=sqlc.arg(ciphertext),
 candidate_code='',candidate_since=NULL,incident_id='',incident_code='',check_at=clock_timestamp(),updated_at=clock_timestamp()
WHERE data_pipeline_alerts.account_id=sqlc.arg(account_id)::text::uuid AND data_pipeline_alerts.revision=sqlc.arg(expected_revision)::bigint
RETURNING *;

-- name: PipelineAlertLock :one
SELECT a.* FROM data_pipeline_alerts a JOIN data_pipelines p ON p.id=a.pipeline_id AND p.account_id=a.account_id
WHERE a.enabled AND a.check_at<=sqlc.arg(now_at) AND a.account_id=ANY(sqlc.arg(accounts)::uuid[])
ORDER BY a.check_at,a.pipeline_id FOR UPDATE OF a SKIP LOCKED LIMIT 1;

-- name: PipelineAlertObserve :exec
UPDATE data_pipeline_alerts SET candidate_code=sqlc.arg(candidate_code),candidate_since=sqlc.narg(candidate_since),incident_id=sqlc.arg(incident_id),incident_code=sqlc.arg(incident_code),check_at=sqlc.arg(check_at)
WHERE pipeline_id=sqlc.arg(pipeline_id)::text::uuid;

-- name: PipelineAlertDisableDeleted :exec
UPDATE data_pipeline_alerts SET enabled=false,ciphertext='',candidate_code='',candidate_since=NULL,incident_id='',incident_code='',revision=revision+1,updated_at=clock_timestamp()
WHERE pipeline_id=sqlc.arg(pipeline_id)::text::uuid;

-- name: PipelineAlertCancelPending :exec
UPDATE data_pipeline_alert_deliveries SET status='cancelled',result=sqlc.arg(result),lease_token='',lease_until=NULL
WHERE pipeline_id=sqlc.arg(pipeline_id)::text::uuid AND status='pending';

-- name: PipelineAlertPendingCount :one
SELECT count(*) FROM data_pipeline_alert_deliveries WHERE pipeline_id=sqlc.arg(pipeline_id)::text::uuid AND status='pending';

-- name: PipelineAlertEnqueue :exec
INSERT INTO data_pipeline_alert_deliveries(id,pipeline_id,account_id,config_revision,incident_id,kind,code,occurred_at,next_attempt_at)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(pipeline_id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(config_revision),sqlc.arg(incident_id)::text::uuid,sqlc.arg(kind),sqlc.arg(code),sqlc.arg(occurred_at),sqlc.arg(occurred_at));

-- name: PipelineAlertHistory :many
SELECT * FROM data_pipeline_alert_deliveries WHERE account_id=sqlc.arg(account_id)::text::uuid AND pipeline_id=sqlc.arg(pipeline_id)::text::uuid ORDER BY sequence DESC LIMIT sqlc.arg(page_limit);

-- name: PipelineAlertPrune :exec
DELETE FROM data_pipeline_alert_deliveries WHERE id IN (SELECT id FROM data_pipeline_alert_deliveries WHERE pipeline_id=sqlc.arg(pipeline_id)::text::uuid AND status<>'pending' ORDER BY sequence DESC OFFSET sqlc.arg(history_offset));

-- name: PipelineAlertClaimDelivery :one
WITH candidate AS (
 SELECT d.id FROM data_pipeline_alert_deliveries d JOIN data_pipeline_alerts a ON a.pipeline_id=d.pipeline_id AND a.revision=d.config_revision
 JOIN data_pipelines p ON p.id=d.pipeline_id AND p.account_id=d.account_id
 WHERE d.status='pending' AND a.enabled AND p.desired_state<>'deleted' AND p.state<>'deleted' AND d.account_id=ANY(sqlc.arg(accounts)::uuid[])
 AND d.next_attempt_at<=sqlc.arg(now_at) AND (d.lease_until IS NULL OR d.lease_until<=sqlc.arg(now_at))
 AND NOT EXISTS(SELECT 1 FROM data_pipeline_alert_deliveries earlier WHERE earlier.pipeline_id=d.pipeline_id AND earlier.sequence<d.sequence AND earlier.status='pending')
 ORDER BY d.next_attempt_at,d.sequence FOR UPDATE OF d SKIP LOCKED LIMIT 1
) UPDATE data_pipeline_alert_deliveries d SET lease_token=sqlc.arg(lease_token),lease_until=sqlc.arg(lease_until)
FROM candidate c WHERE d.id=c.id RETURNING d.*;

-- name: PipelineAlertFinishDelivery :execrows
UPDATE data_pipeline_alert_deliveries SET status=sqlc.arg(status),attempts=attempts+1,response_code=sqlc.arg(response_code),result=sqlc.arg(result),next_attempt_at=sqlc.arg(next_attempt_at),lease_token='',lease_until=NULL
WHERE id=sqlc.arg(id)::text::uuid AND status='pending' AND lease_token=sqlc.arg(lease_token) AND lease_until>sqlc.arg(now_at)
AND EXISTS(SELECT 1 FROM data_pipeline_alerts a WHERE a.pipeline_id=data_pipeline_alert_deliveries.pipeline_id AND a.enabled AND a.revision=data_pipeline_alert_deliveries.config_revision);
