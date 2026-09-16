-- +goose Up
-- +goose StatementBegin
-- Repair deployments that reached a terminal status before every imaged
-- failure path used MarkDeploymentStageFailed. Those rows retained a
-- stage_state.current value forever, so the CLI and dashboard rendered an
-- in-progress stage after the deployment had already stopped.
--
-- The WHERE clause makes this replay-safe: after the first application the
-- current stage is empty and a second application is a no-op.
WITH terminal AS (
    SELECT
        id,
        stage_state,
        COALESCE(
            NULLIF(stage_state->>'current_started_at', '')::timestamptz,
            created_at
        ) AS stage_started_at,
        COALESCE(rollout_aborted_at, rollout_completed_at, cancelled_at, created_at) AS terminal_at,
        CASE
            WHEN status = 'failed' THEN 'failed'
            WHEN status IN ('cancelled', 'superseded') THEN 'cancelled'
            ELSE 'completed'
        END AS stage_status,
        CASE
            WHEN status = 'failed' THEN COALESCE(NULLIF(error_code, ''), NULLIF(error, ''), 'deployment failed')
            WHEN status = 'cancelled' THEN 'deployment cancelled: ' || COALESCE(NULLIF(cancel_reason, ''), 'system')
            WHEN status = 'superseded' THEN 'deployment superseded'
            ELSE NULL
        END AS stage_reason
    FROM deployments
    WHERE status IN ('live', 'failed', 'superseded', 'cancelled')
      AND COALESCE(stage_state->>'current', '') <> ''
), repaired AS (
    SELECT
        id,
        jsonb_set(
            jsonb_set(
                jsonb_set(
                    stage_state,
                    '{history}',
                    jsonb_path_query_array(
                        COALESCE(stage_state->'history', '[]'::jsonb),
                        '$[last - 62 to last]'
                    ) ||
                    jsonb_build_array(
                        jsonb_strip_nulls(jsonb_build_object(
                            'name', stage_state->>'current',
                            'started_at', to_jsonb(stage_started_at),
                            'ended_at', to_jsonb(GREATEST(stage_started_at, terminal_at)),
                            'duration_ms', GREATEST(0, floor(extract(epoch FROM (terminal_at - stage_started_at)) * 1000)::bigint),
                            'status', stage_status,
                            'reason', stage_reason
                        ))
                    ),
                    true
                ),
                '{current}', '""'::jsonb, true
            ),
            '{current_started_at}', 'null'::jsonb, true
        ) AS stage_state
    FROM terminal
)
UPDATE deployments AS d
SET stage_state = repaired.stage_state
FROM repaired
WHERE d.id = repaired.id;
-- +goose StatementEnd

-- +goose Down
-- Historical stage repair is intentionally irreversible.
SELECT 1;
