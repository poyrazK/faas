-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- A controller using an older receipt format cannot reopen an accepted
-- pause. Only a resume from a settled pause can clear the pause marker.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_project_rollback_pause() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.receipt ? 'pause_requested_at' AND (
        (NEW.receipt->>'pause_requested_at' IS DISTINCT FROM OLD.receipt->>'pause_requested_at'
         AND NOT (OLD.status = 'blocked' AND NEW.status = 'running' AND
                  NEW.receipt->>'phase' IS NOT DISTINCT FROM 'preparing'))
        OR (OLD.status = 'running' AND
            (NEW.receipt->>'phase' IS DISTINCT FROM 'releasing' OR NEW.status = 'complete'))
    ) THEN
        RETURN NULL;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgrelid = 'project_rollback_operations'::regclass AND tgname = 'preserve_project_rollback_pause'
    ) THEN
        CREATE TRIGGER preserve_project_rollback_pause
            BEFORE UPDATE OF status, receipt ON project_rollback_operations
            FOR EACH ROW EXECUTE FUNCTION preserve_project_rollback_pause();
    END IF;
END $$;
-- +goose StatementEnd

-- Early durable project rollbacks did not link their checked children.
-- Match their saved attempt and deployment pair, then take the same
-- receipt lock that the pause takes before permitting a traffic increase.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_legacy_project_rollback_pause() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE request_id text := nullif(current_setting('faas.checked_rollback_request', true), '');
        paused boolean;
BEGIN
    IF request_id IS NULL OR NEW.status <> 'live'
       OR NEW.traffic_percent <= (CASE WHEN OLD.status = 'live' THEN OLD.traffic_percent ELSE 0 END)
    THEN
        RETURN NEW;
    END IF;
    SELECT p.receipt ? 'pause_requested_at' INTO paused
    FROM deployment_rollback_operations r
    JOIN project_rollback_operations p ON (p.status = 'running' OR p.receipt ? 'pause_requested_at')
       AND p.receipt->'steps' @> jsonb_build_array(jsonb_build_object(
           'attempt_id', r.id, 'app_id', r.app_id, 'target_deployment_id', r.target_deployment_id,
           'expected_current_deployment_id', r.current_deployment_id))
    WHERE r.id = request_id::uuid AND r.app_id = NEW.app_id AND r.target_deployment_id = NEW.id
      AND r.project_rollback_id IS NULL AND r.status IN ('ready', 'blocked')
    FOR UPDATE OF p;
    IF paused THEN
        RAISE EXCEPTION 'project rollback is paused'
            USING ERRCODE = '23514', CONSTRAINT = 'checked_rollback_required';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgrelid = 'deployments'::regclass AND tgname = 'guard_legacy_project_rollback_pause'
    ) THEN
        CREATE TRIGGER guard_legacy_project_rollback_pause
            BEFORE UPDATE OF status,traffic_percent ON deployments
            FOR EACH ROW EXECUTE FUNCTION guard_legacy_project_rollback_pause();
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER guard_legacy_project_rollback_pause ON deployments;
DROP FUNCTION guard_legacy_project_rollback_pause();
DROP TRIGGER preserve_project_rollback_pause ON project_rollback_operations;
DROP FUNCTION preserve_project_rollback_pause();
