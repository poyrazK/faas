-- +goose Up
-- +goose StatementBegin
-- A failed deployment must not retain an in-progress customer stage. The
-- trigger protects rolling upgrades where an older imaged/apid binary still
-- performs the status flip and stage stamp as separate writes.
CREATE OR REPLACE FUNCTION enforce_failed_deployment_stage_fence() RETURNS trigger AS $$
DECLARE
    terminal_at timestamptz;
    stage_started_at timestamptz;
    duration_ms bigint;
    failure_detail text;
BEGIN
    IF NEW.status = 'failed' AND COALESCE(NEW.stage_state->>'current', '') <> '' THEN
        terminal_at := COALESCE(NEW.rollout_aborted_at, now());
        stage_started_at := COALESCE(
            NULLIF(NEW.stage_state->>'current_started_at', '')::timestamptz,
            NEW.created_at,
            terminal_at
        );
        duration_ms := GREATEST(
            0,
            floor(extract(epoch FROM (GREATEST(stage_started_at, terminal_at) - stage_started_at)) * 1000)::bigint
        );
        failure_detail := COALESCE(
            NULLIF(NEW.error_code, ''),
            NULLIF(NEW.error, ''),
            'deployment failed'
        );
        NEW.stage_state := jsonb_set(
            jsonb_set(
                jsonb_set(
                    NEW.stage_state,
                    '{history}',
                    jsonb_path_query_array(
                        COALESCE(NEW.stage_state->'history', '[]'::jsonb),
                        '$[last - 62 to last]'
                    ) || jsonb_build_array(jsonb_build_object(
                        'name', NEW.stage_state->>'current',
                        'started_at', to_jsonb(stage_started_at),
                        'ended_at', to_jsonb(GREATEST(stage_started_at, terminal_at)),
                        'duration_ms', duration_ms,
                        'status', 'failed',
                        'reason', failure_detail
                    )),
                    true
                ),
                '{current}', '""'::jsonb, true
            ),
            '{current_started_at}', 'null'::jsonb, true
        );
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS deployments_failed_stage_fence ON deployments;
CREATE TRIGGER deployments_failed_stage_fence
BEFORE INSERT OR UPDATE OF status, stage_state ON deployments
FOR EACH ROW EXECUTE FUNCTION enforce_failed_deployment_stage_fence();

-- Replay the trigger for any rows that became terminal before this fence was
-- installed. The update is idempotent because the trigger clears current.
UPDATE deployments
SET status = status
WHERE status = 'failed'
  AND COALESCE(stage_state->>'current', '') <> '';

ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_failed_stage_fence_chk;
ALTER TABLE deployments
    ADD CONSTRAINT deployments_failed_stage_fence_chk CHECK (
        status <> 'failed' OR COALESCE(stage_state->>'current', '') = ''
    );

-- +goose Down
ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_failed_stage_fence_chk;
DROP TRIGGER IF EXISTS deployments_failed_stage_fence ON deployments;
DROP FUNCTION IF EXISTS enforce_failed_deployment_stage_fence();
