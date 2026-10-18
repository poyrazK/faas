-- name: LockNodeAdmissionPolicy :exec
SELECT node_admission_lock_capacity();

-- name: LockNodeAdmissionTarget :one
SELECT lifecycle::text,lifecycle_generation FROM compute_nodes
WHERE id=sqlc.arg(node_id)::uuid FOR SHARE;
