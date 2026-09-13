-- +goose Up
-- Cancelling a deployment terminates every customer-visible release state in
-- the same transaction: it cannot retain traffic, an active rollout, or an
-- in-progress deployment stage. The trigger protects rolling upgrades where
-- an older apid binary may still issue the narrower status-only UPDATE.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enforce_cancelled_deployment_release_fence() RETURNS trigger AS $$
DECLARE
    terminal_at timestamptz;
    cancel_detail text;
    stage_started_at timestamptz;
    duration_ms bigint;
BEGIN
    IF NEW.status = 'cancelled' THEN
        terminal_at := COALESCE(NEW.cancelled_at, now());
        cancel_detail := 'deployment cancelled: ' || COALESCE(NULLIF(NEW.cancel_reason, ''), 'system');
        NEW.cancelled_at := terminal_at;
        NEW.traffic_percent := 0;
        NEW.rollout_state := 'aborted';
        NEW.rollout_completed_at := NULL;
        NEW.rollout_aborted_at := COALESCE(NEW.rollout_aborted_at, terminal_at);
        NEW.rollout_aborted_reason := COALESCE(NULLIF(NEW.rollout_aborted_reason, ''), cancel_detail);

        IF COALESCE(NEW.stage_state->>'current', '') <> '' THEN
            stage_started_at := COALESCE(
				NULLIF(NEW.stage_state->>'current_started_at', '')::timestamptz,
                NEW.created_at,
                terminal_at
            );
            duration_ms := GREATEST(0, floor(extract(epoch FROM (terminal_at - stage_started_at)) * 1000)::bigint);
            NEW.stage_state := jsonb_set(
                jsonb_set(
                    jsonb_set(
                        NEW.stage_state,
                        '{history}',
                        COALESCE(NEW.stage_state->'history', '[]'::jsonb) || jsonb_build_array(jsonb_build_object(
                            'name', NEW.stage_state->>'current',
                            'started_at', to_jsonb(stage_started_at),
                            'ended_at', to_jsonb(terminal_at),
                            'duration_ms', duration_ms,
                            'status', 'cancelled',
                            'reason', cancel_detail
                        )),
                        true
                    ),
                    '{current}', '""'::jsonb, true
                ),
                '{current_started_at}', 'null'::jsonb, true
            );
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS deployments_cancelled_release_fence ON deployments;
CREATE TRIGGER deployments_cancelled_release_fence
BEFORE INSERT OR UPDATE OF status, traffic_percent, rollout_state, rollout_aborted_at, stage_state ON deployments
FOR EACH ROW EXECUTE FUNCTION enforce_cancelled_deployment_release_fence();

-- Repair retained cancellation state from deployments created before the
-- invariant. The trigger closes an active stage while this UPDATE repairs the
-- traffic and rollout projections.
UPDATE deployments
SET traffic_percent = 0,
    rollout_state = 'aborted',
    rollout_completed_at = NULL,
    rollout_aborted_at = COALESCE(rollout_aborted_at, cancelled_at, now()),
    rollout_aborted_reason = COALESCE(NULLIF(rollout_aborted_reason, ''), 'deployment cancelled: ' || COALESCE(NULLIF(cancel_reason, ''), 'system'))
WHERE status = 'cancelled';

ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_cancelled_release_fence_chk;
ALTER TABLE deployments
    ADD CONSTRAINT deployments_cancelled_release_fence_chk CHECK (
        status <> 'cancelled' OR (
            traffic_percent = 0 AND
            rollout_state = 'aborted' AND
            rollout_aborted_at IS NOT NULL AND
            COALESCE(stage_state->>'current', '') = ''
        )
    );

-- +goose Down
ALTER TABLE deployments
    DROP CONSTRAINT IF EXISTS deployments_cancelled_release_fence_chk;
DROP TRIGGER IF EXISTS deployments_cancelled_release_fence ON deployments;
DROP FUNCTION IF EXISTS enforce_cancelled_deployment_release_fence();
