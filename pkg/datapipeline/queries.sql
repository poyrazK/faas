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
 AND NOT EXISTS(SELECT 1 FROM data_pipelines p WHERE p.source_connection_id=c.id AND p.state<>'deleted');

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
UPDATE data_pipelines SET desired_state=sqlc.arg(desired_state),state=sqlc.arg(state),generation=generation+sqlc.arg(generation_delta)::bigint,
 initialized=CASE WHEN sqlc.arg(generation_delta)::bigint>0 THEN false ELSE initialized END,
 revision=revision+1,verify_requested=sqlc.arg(verify_requested),verification=CASE WHEN sqlc.arg(generation_delta)::bigint>0 THEN 'unknown' WHEN sqlc.arg(verify_requested) THEN 'pending' ELSE verification END,
 verified_at=CASE WHEN sqlc.arg(verify_requested) OR sqlc.arg(generation_delta)::bigint>0 THEN NULL ELSE verified_at END,
 retry_at=clock_timestamp()
WHERE account_id=sqlc.arg(account_id)::text::uuid AND id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision) AND state<>'deleted'
RETURNING *;

-- name: PipelineClaim :one
WITH candidate AS (
 SELECT id FROM data_pipelines WHERE state<>'deleted' AND retry_at<=clock_timestamp()
 AND (lease_until IS NULL OR lease_until<=clock_timestamp())
 ORDER BY retry_at,id FOR UPDATE SKIP LOCKED LIMIT 1
) UPDATE data_pipelines p SET lease_token=sqlc.arg(lease_token),lease_until=clock_timestamp()+interval '60 seconds'
 FROM candidate c WHERE p.id=c.id RETURNING p.*;

-- name: PipelineFinish :execrows
UPDATE data_pipelines SET observed_revision=revision,initialized=initialized OR sqlc.arg(initialized),state=sqlc.arg(state),reason=sqlc.arg(reason),tables_ready=sqlc.arg(tables_ready),
 retained_wal_bytes=sqlc.narg(retained_wal_bytes),observed_at=clock_timestamp(),
 verification=sqlc.arg(verification),verified_at=sqlc.narg(verified_at),verify_requested=(sqlc.arg(verification)='pending'),
 lease_token='',lease_until=NULL,retry_at=clock_timestamp()+interval '15 seconds'
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid
 AND revision=sqlc.arg(revision) AND lease_token=sqlc.arg(lease_token) AND lease_until>clock_timestamp();

-- name: PipelineRelease :exec
UPDATE data_pipelines SET lease_token='',lease_until=NULL WHERE id=sqlc.arg(id)::text::uuid AND lease_token=sqlc.arg(lease_token);

-- name: PipelineTargetInUse :one
SELECT EXISTS(SELECT 1 FROM data_pipelines WHERE account_id=sqlc.arg(account_id)::text::uuid AND database_id=sqlc.arg(database_id)::text::uuid AND state<>'deleted');

-- name: PipelinePruneHistory :exec
DELETE FROM data_pipelines WHERE id IN (SELECT id FROM data_pipelines WHERE account_id=sqlc.arg(account_id)::text::uuid AND state='deleted' ORDER BY created_at DESC,id OFFSET sqlc.arg(history_offset) LIMIT sqlc.arg(page_limit));
