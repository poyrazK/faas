-- ADR-1445: post-release watch.

-- name: ListPostReleaseWatchCandidates :many
-- Completed live releases that kept automatic rollback, finished within the
-- watch window (plus telemetry grace), are the newest deployment in their
-- scope, and have a superseded predecessor created before them. Being the
-- newest excludes rollback targets, which are older than what they replaced.
-- An immediate release may not stamp rollout_started_at; its baseline then
-- ends when it completed.
-- The predecessor is the release a plain rollback returns to
-- (LatestRetainedRollbackDeployment): the one that served most recently, never
-- a canary candidate aborted before it completed. Ordered by created_at, a
-- canary aborted between v1 and v3 (retired as superseded when v3 completed)
-- or a build superseded before it went live was chosen, so the watch compared
-- v3 with a release that never served and rolled back to it (release review
-- 2026-10-28).
SELECT d.id, d.app_id, d.scope, d.created_at,
       COALESCE(d.rollout_started_at, d.rollout_completed_at)::timestamptz AS rollout_started_at, d.rollout_completed_at,
       pred.id AS predecessor_id
  FROM deployments AS d
  JOIN LATERAL (
        SELECT p.id FROM deployments AS p
         WHERE p.app_id = d.app_id
           AND COALESCE(NULLIF(p.scope, ''), 'default') = COALESCE(NULLIF(d.scope, ''), 'default')
           AND p.status = 'superseded' AND p.created_at < d.created_at
           AND NOT (p.canary_total_steps > 0 AND p.rollout_state = 'aborted' AND p.rollout_completed_at IS NULL)
         ORDER BY COALESCE(p.serving_ended_at, p.created_at) DESC, p.created_at DESC, p.id DESC LIMIT 1) AS pred ON true
 WHERE d.status = 'live'
   AND d.rollback_on_5xx
   AND d.last_auto_rollback_reason IS NULL
   AND d.traffic_percent = 100
   AND d.rollout_state = 'complete'
   AND d.rollout_completed_at IS NOT NULL
   AND d.rollout_completed_at > now() - make_interval(secs => sqlc.arg('window_seconds')::int)
   AND NOT EXISTS (
        SELECT 1 FROM deployments AS n
         WHERE n.app_id = d.app_id
           AND COALESCE(NULLIF(n.scope, ''), 'default') = COALESCE(NULLIF(d.scope, ''), 'default')
           AND n.created_at > d.created_at
           AND n.status NOT IN ('failed', 'cancelled'))
 ORDER BY d.rollout_completed_at, d.id
 LIMIT sqlc.arg('row_limit')::int;

-- name: RecordPostReleaseRolloutDecision :execrows
-- Records the watch's explained decision (ADR-1447 shape) on the release.
UPDATE deployments SET rollout_decision = sqlc.arg(decision)::jsonb
 WHERE id = sqlc.arg(id)::uuid;
