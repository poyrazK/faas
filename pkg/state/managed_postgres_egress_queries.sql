-- ADR-20261026150000: managed database endpoints, recorded when a credential
-- is sealed, and the bound apps that may reach them.

-- name: RecordManagedPostgresDatabaseEndpoint :exec
INSERT INTO managed_postgres_database_endpoints (database_id, host, port)
VALUES (sqlc.arg(database_id), sqlc.arg(host), sqlc.arg(port))
ON CONFLICT (database_id, host, port) DO UPDATE SET updated_at = now();

-- name: ListManagedPostgresEgressEndpoints :many
SELECT DISTINCT b.app_id, e.host, e.port
FROM managed_postgres_bindings b
JOIN managed_postgres_database_endpoints e ON e.database_id = b.database_id
WHERE b.state IN ('ready', 'retiring') AND b.deleted_at IS NULL
ORDER BY e.host, b.app_id, e.port
LIMIT 100000;
