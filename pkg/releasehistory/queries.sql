-- name: InterruptInProgressActivations :execrows
-- The deploy lock allows one activation per host, so an in-progress row
-- found at the start of the next one belongs to a deployctl that died.
UPDATE platform_release_activations
   SET status = 'interrupted',
       finished_at = GREATEST(sqlc.arg(finished_at)::timestamptz, started_at),
       detail = 'deployctl stopped before it recorded an outcome'
 WHERE host = sqlc.arg(host)
   AND status = 'in_progress';

-- name: StartActivation :one
INSERT INTO platform_release_activations (
    host, kind, release_id, commit_sha, previous_release_id, services, actor, status, started_at
) VALUES (
    sqlc.arg(host), sqlc.arg(kind), sqlc.arg(release_id), sqlc.narg(commit_sha), sqlc.narg(previous_release_id),
    sqlc.narg(services)::text[], sqlc.arg(actor), 'in_progress', sqlc.arg(started_at)
)
RETURNING id;

-- name: FinishActivation :execrows
UPDATE platform_release_activations
   SET status = sqlc.arg(status),
       detail = sqlc.arg(detail),
       finished_at = GREATEST(sqlc.arg(finished_at)::timestamptz, started_at)
 WHERE id = sqlc.arg(id)
   AND status = 'in_progress';

-- name: InsertFinishedActivation :one
-- An activation whose start could not be recorded: on the first rollout the
-- table only exists once the release's migrations have run.
INSERT INTO platform_release_activations (
    host, kind, release_id, commit_sha, previous_release_id, services, actor, status, detail, started_at, finished_at
) VALUES (
    sqlc.arg(host), sqlc.arg(kind), sqlc.arg(release_id), sqlc.narg(commit_sha), sqlc.narg(previous_release_id),
    sqlc.narg(services)::text[], sqlc.arg(actor), sqlc.arg(status), sqlc.arg(detail), sqlc.arg(started_at),
    GREATEST(sqlc.arg(finished_at)::timestamptz, sqlc.arg(started_at))
)
RETURNING id;

-- name: ListActivations :many
SELECT id, host, kind, release_id, commit_sha, previous_release_id, services, actor, status, detail, started_at, finished_at
  FROM platform_release_activations
 WHERE (sqlc.narg(host)::text IS NULL OR host = sqlc.narg(host))
 ORDER BY started_at DESC, id DESC
 LIMIT sqlc.arg(row_limit);

-- name: ImportActivation :execrows
-- A finished activation recorded on a host without owner access to the
-- history (a compute host's spool), under the id it got there: importing it
-- again is a no-op.
INSERT INTO platform_release_activations (
    id, host, kind, release_id, commit_sha, previous_release_id, services, actor, status, detail, started_at, finished_at
) VALUES (
    sqlc.arg(id), sqlc.arg(host), sqlc.arg(kind), sqlc.arg(release_id), sqlc.narg(commit_sha), sqlc.narg(previous_release_id),
    sqlc.narg(services)::text[], sqlc.arg(actor), sqlc.arg(status), sqlc.arg(detail), sqlc.arg(started_at),
    GREATEST(sqlc.arg(finished_at)::timestamptz, sqlc.arg(started_at))
)
ON CONFLICT (id) DO NOTHING;
