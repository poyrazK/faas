-- name: InsertPromotionHealth :exec
INSERT INTO project_environment_promotion_health(promotion_id,generation,phase,payload)
VALUES(sqlc.arg(promotion_id)::text::uuid,sqlc.arg(generation)::bigint,sqlc.arg(phase)::text,sqlc.arg(payload)::jsonb);

-- name: PromotionHealthProject :one
SELECT project_id::text FROM project_environment_promotions
WHERE id=sqlc.arg(promotion_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;

-- name: ClaimPromotionHealth :execrows
WITH owner AS MATERIALIZED (
 SELECT id FROM project_environment_promotions WHERE id=sqlc.arg(promotion_id)::text::uuid
 AND account_id=sqlc.arg(account_id)::text::uuid AND (status='running' OR status='failed' AND EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries r WHERE r.promotion_id=project_environment_promotions.id AND r.status IN ('quiescing','verifying'))) FOR UPDATE
), claimed AS (
 UPDATE project_environment_promotion_health h SET lease_token=sqlc.arg(lease_token)::text::uuid,
 lease_until=clock_timestamp()+make_interval(secs=>sqlc.arg(lease_seconds)::double precision)
 FROM owner WHERE owner.id=h.promotion_id AND h.lease_until<clock_timestamp() RETURNING h.promotion_id
)
UPDATE project_environment_promotions p SET updated_at=clock_timestamp()
FROM claimed WHERE p.id=claimed.promotion_id;

-- name: ReleasePromotionHealth :exec
UPDATE project_environment_promotion_health SET lease_token=NULL,lease_until='-infinity',next_check_at=sqlc.arg(next_check_at)::timestamptz
WHERE promotion_id=sqlc.arg(promotion_id)::text::uuid AND lease_token=sqlc.arg(lease_token)::text::uuid;

-- name: LockPromotionHealthOwner :one
SELECT id::text,account_id::text,project_id::text,project_slug,from_environment,to_environment,status,release_graph_mode,
coalesce(source_release_set_id::text,'')::text AS source_release_id,
coalesce(target_release_set_id::text,'')::text AS target_release_id,
coalesce(previous_target_release_set_id::text,'')::text AS previous_release_id,
coalesce(restored_target_release_set_id::text,'')::text AS restored_release_id,
coalesce(source_qualification_id::text,'')::text AS qualification_id,release_ttl_seconds,
rollback_status,sync_config,source_config_hash,previous_target_config_hash,target_config_version,rollback_config_version,
source_config_snapshot,previous_target_config_snapshot
FROM project_environment_promotions
WHERE id=sqlc.arg(promotion_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid FOR UPDATE;

-- name: ReadPromotionHealth :one
SELECT h.payload FROM project_environment_promotion_health h JOIN project_environment_promotions p ON p.id=h.promotion_id
WHERE p.id=sqlc.arg(promotion_id)::text::uuid AND p.account_id=sqlc.arg(account_id)::text::uuid;

-- name: SavePromotionHealth :execrows
UPDATE project_environment_promotion_health SET generation=sqlc.arg(generation)::bigint,phase=sqlc.arg(phase)::text,payload=sqlc.arg(payload)::jsonb
WHERE promotion_id=sqlc.arg(promotion_id)::text::uuid AND generation=sqlc.arg(previous_generation)::bigint;

-- name: PendingPromotionHealth :many
SELECT promotion_id::text,account_id::text,project_slug,to_environment
FROM project_promotion_health_work
WHERE next_check_at<=clock_timestamp() AND lease_until<=clock_timestamp()
ORDER BY next_check_at,last_claimed_at,promotion_id LIMIT sqlc.arg(batch_size)::int;

-- name: UpdatePromotionHealthResult :exec
UPDATE project_environment_promotions SET status=sqlc.arg(status)::text,error=sqlc.arg(error_message)::text,
 verification_status=sqlc.arg(verification_status)::text,verification_error=sqlc.arg(error_message)::text,
 verification_started_at=CASE WHEN EXISTS(SELECT 1 FROM project_environment_promotion_health h WHERE h.promotion_id=project_environment_promotions.id AND h.payload ? 'admission' AND h.payload#>>'{admission,started_at}' IS NULL) THEN verification_started_at ELSE coalesce(verification_started_at,now()) END,
 verification_completed_at=CASE WHEN sqlc.arg(status)::text<>'running' THEN now() ELSE NULL END,
 completed_at=CASE WHEN sqlc.arg(status)::text<>'running' THEN now() ELSE NULL END,updated_at=now()
WHERE id=sqlc.arg(promotion_id)::text::uuid;

-- name: PromotionMigrationAdmissionAllowed :one
SELECT NOT EXISTS(SELECT 1 FROM project_environment_promotion_migrations m JOIN project_environment_promotions p ON p.id=m.promotion_id WHERE p.project_id=sqlc.arg(project_id)::text::uuid AND p.id<>sqlc.arg(promotion_id)::text::uuid AND (p.status='running' OR EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries r WHERE r.promotion_id=p.id AND r.status IN ('quiescing','verifying'))))
 AND NOT EXISTS(SELECT 1 FROM project_submissions s WHERE s.payload#>>'{apply,project_id}'=sqlc.arg(project_id)::text AND s.payload#>>'{apply,safe_release,release_command,compatibility}'='backward-compatible' AND s.payload#>>'{apply,safe_release,status}' IN ('building','verifying','rolling_back'))
 AND NOT EXISTS(SELECT 1 FROM app_tasks t JOIN apps a ON a.id=t.app_id WHERE a.project_id=sqlc.arg(project_id)::text::uuid AND t.kind='release' AND (t.status IN ('queued','restoring','running') OR t.started_at IS NOT NULL AND t.exit_code IS NULL AND NOT EXISTS(SELECT 1 FROM project_promotion_migration_resolved_tasks resolved WHERE resolved.task_id=t.id)))
 AND NOT EXISTS(SELECT 1 FROM project_migration_pending_contracts c WHERE c.project_id=sqlc.arg(project_id)::text::uuid) AS allowed;

-- name: InsertPromotionMigration :exec
INSERT INTO project_environment_promotion_migrations(promotion_id) VALUES(sqlc.arg(promotion_id)::text::uuid);

-- name: ReadPromotionMigration :one
SELECT coalesce(task_id::text,'')::text AS task_id,runtime_fingerprint,coalesce(prepared_lease_token::text,'')::text AS prepared_lease_token FROM project_environment_promotion_migrations WHERE promotion_id=sqlc.arg(promotion_id)::text::uuid;

-- name: ReadPromotionMigrationForTask :one
SELECT p.id::text AS promotion_id,p.account_id::text AS account_id,p.project_id::text AS project_id,p.to_environment,m.runtime_fingerprint,coalesce(m.prepared_lease_token::text,'')::text AS prepared_lease_token
FROM project_environment_promotion_migrations m JOIN project_environment_promotions p ON p.id=m.promotion_id WHERE m.task_id=sqlc.arg(task_id)::text::uuid;

-- name: CapturePromotionMigrationTask :execrows
UPDATE project_environment_promotion_migrations SET task_id=sqlc.arg(task_id)::text::uuid,runtime_fingerprint=sqlc.arg(fingerprint)::text
WHERE promotion_id=sqlc.arg(promotion_id)::text::uuid AND task_id IS NULL;

-- name: AttestPromotionMigrationRuntime :exec
UPDATE project_environment_promotion_migrations SET prepared_lease_token=sqlc.arg(token)::text::uuid
WHERE task_id=sqlc.arg(task_id)::text::uuid AND runtime_fingerprint=sqlc.arg(fingerprint)::text;

-- name: InsertPromotionMigrationTask :exec
INSERT INTO app_tasks(id,account_id,app_id,deployment_id,kind,command,command_shell,deployment_scope,artifact_key,image_digest,timeout_seconds,max_output_bytes,start_deadline_at)
SELECT sqlc.arg(task_id)::text::uuid,a.account_id,d.app_id,d.id,'release',d.release_command,d.release_command_shell,d.scope,d.rootfs_key,d.image_digest,sqlc.arg(timeout_seconds)::int,sqlc.arg(output_bytes)::int,sqlc.arg(start_deadline)::timestamptz
FROM deployments d JOIN apps a ON a.id=d.app_id WHERE d.id=sqlc.arg(deployment_id)::text::uuid AND d.status='live' AND d.traffic_percent=0 AND d.traffic_percent_explicit AND a.status<>'deleted';

-- name: ReadPromotionMigrationOwner :one
SELECT project_id::text,from_environment,to_environment,status,coalesce(source_release_set_id::text,'')::text AS source_release_id,coalesce(previous_target_release_set_id::text,'')::text AS previous_release_id
FROM project_environment_promotions WHERE id=sqlc.arg(promotion_id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid;
