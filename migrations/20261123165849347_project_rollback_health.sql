-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Older workers can renew leases, but their receipt format cannot save an
-- opted-in operation or discard its pinned health policy.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION preserve_project_rollback_health_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.receipt->>'verify' = 'true' AND (
        NEW.receipt->>'verify' IS DISTINCT FROM 'true'
        OR NEW.receipt->'health_probes' IS DISTINCT FROM OLD.receipt->'health_probes'
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
        WHERE tgrelid = 'project_rollback_operations'::regclass AND tgname = 'preserve_project_rollback_health_policy'
    ) THEN
        CREATE TRIGGER preserve_project_rollback_health_policy
            BEFORE UPDATE OF receipt ON project_rollback_operations
            FOR EACH ROW EXECUTE FUNCTION preserve_project_rollback_health_policy();
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_verified_project_rollback() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'live' AND NEW.traffic_percent > (CASE WHEN OLD.status = 'live' THEN OLD.traffic_percent ELSE 0 END)
       AND EXISTS (
           SELECT 1 FROM deployment_rollback_operations r
           JOIN project_rollback_operations p ON p.id = r.project_rollback_id
           WHERE r.app_id = NEW.app_id AND r.target_deployment_id = NEW.id
             AND r.status NOT IN ('complete', 'failed') AND p.receipt->>'verify' = 'true'
             AND r.id::text IS DISTINCT FROM current_setting('faas.project_rollback_verified_cutover', true)
       ) THEN
        RAISE EXCEPTION 'verified project rollback requires health authorization'
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
        WHERE tgrelid = 'deployments'::regclass AND tgname = 'guard_verified_project_rollback'
    ) THEN
        CREATE TRIGGER guard_verified_project_rollback
            BEFORE UPDATE OF status,traffic_percent ON deployments
            FOR EACH ROW EXECUTE FUNCTION guard_verified_project_rollback();
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER guard_verified_project_rollback ON deployments;
DROP FUNCTION guard_verified_project_rollback();
DROP TRIGGER preserve_project_rollback_health_policy ON project_rollback_operations;
DROP FUNCTION preserve_project_rollback_health_policy();
