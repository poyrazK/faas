-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_project_rollback_live_traffic_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.receipt->>'verify_live_traffic' = 'true' AND (
        NEW.receipt->>'verify_live_traffic' IS DISTINCT FROM 'true'
        OR NEW.receipt->'live_traffic_policies' IS DISTINCT FROM OLD.receipt->'live_traffic_policies'
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
        WHERE tgrelid = 'project_rollback_operations'::regclass AND tgname = 'preserve_project_rollback_live_traffic_policy'
    ) THEN
        CREATE TRIGGER preserve_project_rollback_live_traffic_policy
            BEFORE UPDATE OF receipt ON project_rollback_operations
            FOR EACH ROW EXECUTE FUNCTION preserve_project_rollback_live_traffic_policy();
    END IF;
END $$;
-- +goose StatementEnd

-- Older checked workers know the probe gate but cannot authorize this gate.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_project_rollback_live_traffic() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'live' AND NEW.traffic_percent > (CASE WHEN OLD.status = 'live' THEN OLD.traffic_percent ELSE 0 END)
       AND EXISTS (
           SELECT 1 FROM deployment_rollback_operations r
           JOIN project_rollback_operations p ON p.id = r.project_rollback_id
           WHERE r.app_id = NEW.app_id AND r.target_deployment_id = NEW.id
             AND r.status NOT IN ('complete', 'failed') AND p.receipt->>'verify_live_traffic' = 'true'
             AND r.id::text IS DISTINCT FROM current_setting('faas.project_rollback_live_traffic_verified', true)
       ) THEN
        RAISE EXCEPTION 'project rollback requires live-traffic health authorization'
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
        WHERE tgrelid = 'deployments'::regclass AND tgname = 'guard_verified_project_rollback_traffic'
    ) THEN
        CREATE TRIGGER guard_verified_project_rollback_traffic
            BEFORE UPDATE OF status,traffic_percent ON deployments
            FOR EACH ROW EXECUTE FUNCTION guard_project_rollback_live_traffic();
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER guard_verified_project_rollback_traffic ON deployments;
DROP FUNCTION guard_project_rollback_live_traffic();
DROP TRIGGER preserve_project_rollback_live_traffic_policy ON project_rollback_operations;
DROP FUNCTION preserve_project_rollback_live_traffic_policy();
