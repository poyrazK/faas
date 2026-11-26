-- Route splits (ADR-20261121170000 amendment 2). apid writes the rows; its
-- worker moves each in-flight split forward with a compare-and-set on phase.

-- name: InsertRouteSplit :one
INSERT INTO route_splits (
    account_id, source_app_id, target_slug, kind, route, method, path_glob,
    route_ram_mb, main_ram_mb, previous_ram_mb
) VALUES (
    @account_id, @source_app_id, @target_slug, @kind, @route, @method, @path_glob,
    @route_ram_mb, @main_ram_mb, @previous_ram_mb
)
RETURNING *;

-- name: ReadRouteSplit :one
SELECT * FROM route_splits WHERE id = @id AND account_id = @account_id;

-- name: ListRouteSplitsForApp :many
SELECT * FROM route_splits
 WHERE account_id = @account_id AND source_app_id = @source_app_id
 ORDER BY created_at DESC, id DESC
 LIMIT @row_limit;

-- name: ListInFlightRouteSplits :many
SELECT * FROM route_splits
 WHERE phase IN ('creating', 'deploying', 'rule_live')
   AND (updated_at, id) > (@after_updated_at::timestamptz, @after_id::uuid)
 ORDER BY updated_at, id
 LIMIT @row_limit;

-- name: AdvanceRouteSplit :one
UPDATE route_splits
   SET target_slug = @target_slug,
       target_app_id = sqlc.narg(target_app_id),
       target_deployment_id = sqlc.narg(target_deployment_id),
       main_resized = @main_resized,
       edge_rule_ids = @edge_rule_ids,
       phase = @phase,
       warning = @warning,
       error = @error,
       lease_until = NULL,
       updated_at = now()
 WHERE id = @id AND account_id = @account_id AND phase = @from_phase
RETURNING *;

-- name: ClaimRouteSplit :one
UPDATE route_splits
   SET lease_until = @lease_until
 WHERE id = @id AND account_id = @account_id AND phase = @phase
   AND (lease_until IS NULL OR lease_until < now())
RETURNING *;

-- name: ReleaseRouteSplit :exec
-- Ends a pass's claim without touching updated_at, which the timeouts run on.
UPDATE route_splits
   SET lease_until = NULL
 WHERE id = @id AND account_id = @account_id AND lease_until = @lease_until;
