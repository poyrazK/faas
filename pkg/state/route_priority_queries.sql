-- Route priorities (ADR-957). apid owns the rows; gatewayd-internal reads them.

-- name: GetRoutePriorities :one
SELECT routes, updated_at FROM app_route_priorities
WHERE app_id = sqlc.arg(app_id) AND account_id = sqlc.arg(account_id);

-- name: SetRoutePriorities :one
INSERT INTO app_route_priorities (app_id, account_id, routes, updated_at)
VALUES (sqlc.arg(app_id), sqlc.arg(account_id), sqlc.arg(routes)::jsonb, clock_timestamp())
ON CONFLICT (app_id) DO UPDATE SET routes = EXCLUDED.routes, updated_at = EXCLUDED.updated_at
WHERE app_route_priorities.account_id = EXCLUDED.account_id
RETURNING routes, updated_at;

-- name: DeleteRoutePriorities :exec
DELETE FROM app_route_priorities WHERE app_id = sqlc.arg(app_id) AND account_id = sqlc.arg(account_id);
