-- name: RequestManagedPostgresCutoverPreflight :exec
INSERT INTO managed_postgres_cutover_preflights(cutover_id,state,revision,created_at,updated_at)
VALUES(sqlc.arg(id)::text::uuid,'pending',1,sqlc.arg(now)::timestamptz,sqlc.arg(now))
ON CONFLICT(cutover_id) DO UPDATE SET state='pending',revision=managed_postgres_cutover_preflights.revision+1,
 binding_ids='{}',blockers='{}',verification_fresh=false,source_matches=false,target_matches=false,bindings_match=false,
 checked_at=NULL,expires_at=NULL,cutover_updated_at=NULL,runtime_drain=NULL,runtime_drain_fingerprint='',source_sessions=NULL,source_sessions_fingerprint='',updated_at=EXCLUDED.updated_at
WHERE managed_postgres_cutover_preflights.state<>'pending';

-- name: GetManagedPostgresCutoverPreflight :one
SELECT p.* FROM managed_postgres_cutover_preflights p JOIN managed_postgres_cutovers c ON c.id=p.cutover_id
WHERE c.account_id=sqlc.arg(account_id)::text::uuid AND c.id=sqlc.arg(id)::text::uuid;

-- name: FinishManagedPostgresCutoverPreflight :one
UPDATE managed_postgres_cutover_preflights SET state='blocked',binding_ids=sqlc.arg(binding_ids)::text[],
 blockers=sqlc.arg(blockers)::text[],verification_fresh=sqlc.arg(verification_fresh)::boolean,
 source_matches=sqlc.arg(source_matches)::boolean,target_matches=sqlc.arg(target_matches)::boolean,bindings_match=sqlc.arg(bindings_match)::boolean,
 cutover_updated_at=sqlc.arg(cutover_updated_at)::timestamptz,checked_at=sqlc.arg(now)::timestamptz,expires_at=sqlc.arg(expires_at)::timestamptz,updated_at=sqlc.arg(now),
 runtime_drain=sqlc.narg(runtime_drain)::jsonb,runtime_drain_fingerprint=sqlc.arg(runtime_drain_fingerprint)::text,
 source_sessions=sqlc.narg(source_sessions)::jsonb,source_sessions_fingerprint=sqlc.arg(source_sessions_fingerprint)::text
WHERE cutover_id=sqlc.arg(id)::text::uuid AND revision=sqlc.arg(revision)::bigint AND state='pending' RETURNING *;

-- name: PendingManagedPostgresCutoverPreflights :many
SELECT c.* FROM managed_postgres_cutover_preflights p JOIN managed_postgres_cutovers c ON c.id=p.cutover_id
WHERE p.state='pending' ORDER BY p.updated_at,p.cutover_id LIMIT sqlc.arg(batch_size)::integer;

-- name: ReadManagedPostgresPreflightDatabases :many
SELECT * FROM managed_postgres_databases WHERE id::text=ANY(sqlc.arg(ids)::text[]);

-- name: ReadManagedPostgresPreflightBindings :many
SELECT * FROM managed_postgres_bindings WHERE database_id=sqlc.arg(database_id)::text::uuid
 AND app_id=sqlc.arg(app_id)::text::uuid AND scope=sqlc.arg(scope)::text AND state<>'deleted' ORDER BY id;

-- name: ReadManagedPostgresPreflightDrainPlan :one
SELECT p.id::text FROM managed_postgres_cutover_drain_plans p JOIN apps a ON a.id=p.app_id
WHERE p.cutover_id=sqlc.arg(cutover_id)::text::uuid AND p.app_id=sqlc.arg(app_id)::text::uuid
ORDER BY (p.owner_node_id=a.node_id AND p.owner_revision=a.scaling_policy_revision
 AND p.fenced_at=a.managed_postgres_admission_fenced_at AND p.cutover_id=a.managed_postgres_admission_cutover_id) DESC NULLS LAST,
 p.created_at DESC,p.id DESC LIMIT 1;
