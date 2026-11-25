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
 ORDER BY updated_at, id
 LIMIT @row_limit;

-- name: AdvanceRouteSplit :one
UPDATE route_splits
   SET target_app_id = sqlc.narg(target_app_id),
       target_deployment_id = sqlc.narg(target_deployment_id),
       main_resized = @main_resized,
       edge_rule_ids = @edge_rule_ids,
       phase = @phase,
       warning = @warning,
       error = @error,
       updated_at = now()
 WHERE id = @id AND account_id = @account_id AND phase = @from_phase
RETURNING *;
