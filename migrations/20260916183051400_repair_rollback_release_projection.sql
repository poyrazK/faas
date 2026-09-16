-- +goose Up
-- Repair impossible release projections left by the historical automatic
-- rollback status-only swap. Terminal deployments cannot own traffic. A
-- scope whose live rows sum to zero is repaired to one deterministic serving
-- row while every sibling is retired, so gateway routing, traffic status,
-- and release inspection converge on the same deployment.
-- +goose StatementBegin
UPDATE deployments
SET traffic_percent = 0
WHERE status IN ('superseded', 'failed', 'cancelled')
  AND traffic_percent <> 0;

WITH zero_live_scopes AS (
    SELECT app_id, scope
    FROM deployments
    WHERE status = 'live'
      AND deleted_at IS NULL
    GROUP BY app_id, scope
    HAVING SUM(traffic_percent) = 0
), ranked AS (
    SELECT
        d.id,
        row_number() OVER (
            PARTITION BY d.app_id, d.scope
            ORDER BY d.created_at DESC, d.id DESC
        ) AS serving_rank
    FROM deployments d
    JOIN zero_live_scopes z
      ON z.app_id = d.app_id AND z.scope = d.scope
    WHERE d.status = 'live'
      AND d.deleted_at IS NULL
)
UPDATE deployments AS d
SET status = CASE WHEN ranked.serving_rank = 1 THEN 'live' ELSE 'superseded' END,
    traffic_percent = CASE WHEN ranked.serving_rank = 1 THEN 100 ELSE 0 END,
    rollout_state = CASE WHEN ranked.serving_rank = 1 THEN 'complete' ELSE 'aborted' END,
    rollout_started_at = CASE
        WHEN ranked.serving_rank = 1 THEN COALESCE(d.rollout_started_at, now())
        ELSE d.rollout_started_at
    END,
    rollout_completed_at = CASE WHEN ranked.serving_rank = 1 THEN now() ELSE NULL END,
    rollout_aborted_at = CASE
        WHEN ranked.serving_rank = 1 THEN NULL
        ELSE COALESCE(d.rollout_aborted_at, now())
    END,
    rollout_aborted_reason = CASE
        WHEN ranked.serving_rank = 1 THEN ''
        ELSE COALESCE(NULLIF(d.rollout_aborted_reason, ''), 'repaired invalid rollback projection')
    END
FROM ranked
WHERE d.id = ranked.id;
-- +goose StatementEnd

-- +goose Down
-- Historical release-projection repair is intentionally irreversible.
SELECT 1;
