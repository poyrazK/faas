-- ADR-20261025094915: managed PostgreSQL compute overage and cap subjects.

-- name: ManagedPostgresOverageEnabled :one
SELECT COALESCE((SELECT o.enabled FROM managed_postgres_account_overage o
 WHERE o.subject_id=sqlc.arg(subject_id)::uuid), false)::boolean AS enabled;

-- name: SetManagedPostgresOverage :exec
INSERT INTO managed_postgres_account_overage (subject_id, enabled, updated_at)
VALUES (sqlc.arg(subject_id)::uuid, sqlc.arg(enabled)::boolean, sqlc.arg(updated_at)::timestamptz)
ON CONFLICT (subject_id) DO UPDATE SET enabled=EXCLUDED.enabled, updated_at=EXCLUDED.updated_at;

-- name: ManagedPostgresUsageSubjects :many
SELECT d.id::text AS database_id, managed_postgres_usage_subject(d.org_id, d.account_id)::text AS subject_id
FROM managed_postgres_databases d
WHERE d.id = ANY(sqlc.arg(database_ids)::uuid[]);
