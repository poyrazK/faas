-- ADR-20261023181931: a rollback database switch activates its cutover.
-- Every query runs inside the activation or resume transaction, after the
-- account, app and cutover locks.

-- name: ManagedPostgresCutoverDrainPlanForFence :one
SELECT id::text AS id FROM managed_postgres_cutover_drain_plans
WHERE cutover_id=sqlc.arg(cutover_id)::text::uuid AND app_id=sqlc.arg(app_id)::text::uuid
 AND fenced_at=sqlc.arg(fenced_at)::timestamptz
ORDER BY created_at DESC,id LIMIT 1;

-- name: ManagedPostgresCutoverUnreleasedSourceFences :one
SELECT count(*) FROM managed_postgres_cutover_source_fences
WHERE cutover_id=sqlc.arg(cutover_id)::text::uuid AND state<>'released';

-- name: ManagedPostgresActivationLiveDeployments :many
SELECT id::text AS id, traffic_percent FROM deployments
WHERE app_id=sqlc.arg(app_id)::text::uuid AND coalesce(nullif(scope,''),'default')=sqlc.arg(scope)::text
 AND status='live' AND traffic_percent>0
ORDER BY id;

-- name: ActivateManagedPostgresCutover :execrows
UPDATE managed_postgres_cutovers SET state='activated',lease_token=NULL,lease_until=NULL,attempt_count=0,
last_error_code=NULL,retry_at=sqlc.arg(now)::timestamptz,updated_at=sqlc.arg(now)::timestamptz
WHERE id=sqlc.arg(id)::text::uuid AND account_id=sqlc.arg(account_id)::text::uuid AND state='verified' AND lease_token IS NULL;

-- name: PublishManagedPostgresCutoverCredentials :execrows
UPDATE managed_postgres_cutover_credentials SET state='published',provider_identity_id=NULL,credential_ref=NULL,
ciphertext=NULL,kid=NULL,value_hash=NULL,verified_at=NULL
WHERE cutover_id=sqlc.arg(cutover_id)::text::uuid AND state='sealed';

-- name: DeleteManagedPostgresBindingSecrets :execrows
DELETE FROM app_secrets WHERE managed_postgres_binding_id=sqlc.arg(binding_id)::text::uuid;

-- name: RetireManagedPostgresCutoverSourceBinding :execrows
-- The binding reconciler finishes the delete: it revokes the provider
-- credentials and marks the binding deleted.
UPDATE managed_postgres_bindings SET state='deleting',lease_token=NULL,lease_until=NULL,attempt_count=0,
last_error_code=NULL,retry_at=sqlc.arg(now)::timestamptz,updated_at=sqlc.arg(now)::timestamptz
WHERE id=sqlc.arg(id)::text::uuid AND state='ready' AND cutover_id IS NULL;

-- name: InsertActivatedManagedPostgresBinding :exec
INSERT INTO managed_postgres_bindings(id,account_id,database_id,app_id,scope,environment_key,access,
 provider_identity_id,credential_ref,credential_generation,state,retry_at,created_at,updated_at)
VALUES(sqlc.arg(id)::text::uuid,sqlc.arg(account_id)::text::uuid,sqlc.arg(database_id)::text::uuid,
 sqlc.arg(app_id)::text::uuid,sqlc.arg(scope)::text,sqlc.arg(environment_key)::text,sqlc.arg(access)::text,
 sqlc.arg(provider_identity_id)::text,sqlc.arg(credential_ref)::text,1,'ready',
 sqlc.arg(now)::timestamptz,sqlc.arg(now)::timestamptz,sqlc.arg(now)::timestamptz);

-- name: LockManagedPostgresActivationSecretTarget :exec
SELECT pg_advisory_xact_lock(managed_secret_target_lock_key(sqlc.arg(app_id)::text,sqlc.arg(scope)::text,sqlc.arg(key)::text));

-- name: InsertActivatedManagedPostgresSecret :exec
INSERT INTO app_secrets(account_id,app_id,scope,key,ciphertext,kid,value_hash,managed_postgres_binding_id,
 managed_credential_ref,managed_credential_generation)
VALUES(sqlc.arg(account_id)::text::uuid,sqlc.arg(app_id)::text::uuid,sqlc.arg(scope)::text,sqlc.arg(key)::text,
 sqlc.arg(ciphertext)::bytea,sqlc.arg(kid)::text,sqlc.arg(value_hash)::text,sqlc.arg(binding_id)::text::uuid,
 sqlc.arg(credential_ref)::text,1);

-- name: StampManagedPostgresActivationRuntimeConfig :exec
INSERT INTO app_runtime_config_changes (app_id, changed_at) VALUES (sqlc.arg(app_id)::text::uuid, now())
ON CONFLICT (app_id) DO UPDATE SET changed_at = excluded.changed_at;

-- name: ReleaseActivatedManagedPostgresCutoverAdmission :execrows
UPDATE apps SET managed_postgres_admission_cutover_id=NULL,managed_postgres_admission_fenced_at=NULL
WHERE id=sqlc.arg(app_id)::text::uuid AND managed_postgres_admission_cutover_id=sqlc.arg(cutover_id)::text::uuid
 AND EXISTS (SELECT 1 FROM managed_postgres_cutovers c WHERE c.id=sqlc.arg(cutover_id)::text::uuid AND c.state='activated');

-- name: PreviewManagedPostgresDrainInventory :many
-- The same inventory a drain plan captures, read without locks, so a
-- database switch can wait for members a drain cannot retire to settle.
SELECT i.id,i.deployment_id,i.node_id,i.wake_id,i.kind,i.mode,i.state,i.migrated_from_node_id,
 coalesce(d.scope,'')::text AS deployment_scope,
 EXISTS (SELECT 1 FROM environment_qualification_executions e WHERE e.instance_id=i.id AND e.retired_at IS NULL) AS qualification_live
FROM instances i LEFT JOIN deployments d ON d.id=i.deployment_id
WHERE i.app_id=sqlc.arg(app_id)::text::uuid ORDER BY i.id LIMIT sqlc.arg(page_limit);
