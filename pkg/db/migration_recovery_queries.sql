-- adr: 429. Administrative migration evidence never creates runtime authority.

-- name: ReadMigrationRecoveryLedger :many
SELECT id,version_id,is_applied,tstamp FROM goose_db_version ORDER BY id;

-- name: ReadMigrationRecoveryIdentity :one
SELECT current_database()::text AS database_name, current_schema()::text AS schema_name,
 current_user::text AS actor, current_setting('server_version_num')::integer AS server_version,
 oid AS database_oid FROM pg_catalog.pg_database WHERE datname=current_database();

-- name: ReadMigrationRecoveryCluster :one
SELECT coalesce(to_jsonb(pg_control_system())->>'system_identifier','')::text AS system_identifier;

-- name: CheckMigrationRecoveryWriters :one
SELECT NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND (c.relowner<>to_regrole(quote_ident(current_user))::oid OR c.relacl IS NOT NULL))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=current_schema() AND (p.proowner<>to_regrole(quote_ident(current_user))::oid OR p.proacl IS NOT NULL))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace
 WHERE n.nspname=current_schema() AND (t.typowner<>to_regrole(quote_ident(current_user))::oid OR t.typacl IS NOT NULL))
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_default_acl a LEFT JOIN pg_catalog.pg_namespace n ON n.oid=a.defaclnamespace
 WHERE a.defaclnamespace=0 OR n.nspname=current_schema())
 AND EXISTS (SELECT 1 FROM pg_catalog.pg_namespace n WHERE n.nspname=current_schema()
  AND pg_has_role(current_user,n.nspowner,'USAGE') AND NOT EXISTS (
   SELECT 1 FROM aclexplode(n.nspacl) a WHERE a.grantee<>n.nspowner
    AND (a.grantee<>0 OR a.privilege_type<>'USAGE' OR a.is_grantable))) AS valid;

-- name: CheckMigrationRecoveryBackfills :one
SELECT (SELECT count(*) FROM apps)::bigint AS application_count,
 NOT EXISTS (SELECT 1 FROM apps a LEFT JOIN app_application_standards e ON e.app_id=a.id
  WHERE a.org_id IS NOT NULL AND (e.app_id IS NULL OR e.org_id IS DISTINCT FROM a.org_id OR e.project_id IS DISTINCT FROM a.project_id
   OR NOT e.base_settings ?& ARRAY['require_signed','security_policy','egress_cidrs','egress_extra_ports']))
 AND NOT EXISTS (SELECT 1 FROM app_application_standards e,jsonb_each(e.effective->'sources') s
  WHERE jsonb_typeof(s.value)='array' AND jsonb_array_length(s.value)>0 AND NOT s.key=ANY(e.materialized_fields)) AS valid;

-- name: LockMigrationRecoveryLedger :exec
LOCK TABLE goose_db_version IN EXCLUSIVE MODE;

-- name: LockMigrationRecoveryBackfills :exec
LOCK TABLE apps,app_application_standards IN SHARE MODE;

-- name: ExportMigrationRecoverySnapshot :one
SELECT pg_export_snapshot()::text AS snapshot;

-- name: RecordRecoveredMigrationVersion :one
INSERT INTO goose_db_version(version_id,is_applied) VALUES($1,true) RETURNING id,version_id,is_applied,tstamp;

-- name: RecordMigrationRecoveryReceipt :one
INSERT INTO application_standard_ledger_recoveries(approval_hash,target_hash,schema_hash,source_hash,ledger_hash,plan,repaired_versions)
VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING approval_hash,target_hash,schema_hash,source_hash,ledger_hash,actor,plan,repaired_versions,recovered_at;

-- name: GetMigrationRecoveryReceipt :one
SELECT approval_hash,target_hash,schema_hash,source_hash,ledger_hash,actor,plan,repaired_versions,recovered_at
FROM application_standard_ledger_recoveries WHERE approval_hash=$1;
