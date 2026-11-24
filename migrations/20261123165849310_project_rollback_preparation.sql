-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE deployment_rollback_operations ADD COLUMN IF NOT EXISTS project_rollback_id uuid;

-- An older controller may still be handling a checked rollback during a
-- rollout. Protect the preparation marker and the project receipt on disk.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION retain_project_rollback_preparation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT p.id INTO NEW.project_rollback_id
        FROM apps a JOIN project_rollback_operations p ON p.project_id = a.project_id
        WHERE a.id = NEW.app_id AND p.status = 'running'
          AND p.receipt->>'phase' IN ('preparing', 'cutover', 'releasing')
          AND EXISTS (
              SELECT 1 FROM jsonb_array_elements(p.receipt->'steps') AS step
              WHERE step->>'attempt_id' = NEW.id::text AND step->>'app_id' = NEW.app_id::text
                AND step->>'target_deployment_id' = NEW.target_deployment_id::text
                AND step->>'expected_current_deployment_id' = NEW.current_deployment_id::text
          );
    ELSE
        NEW.project_rollback_id := OLD.project_rollback_id;
    END IF;
    IF NEW.project_rollback_id IS NOT NULL THEN
        NEW.receipt := NEW.receipt || jsonb_build_object('project_rollback_id', NEW.project_rollback_id);
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgrelid = 'deployment_rollback_operations'::regclass AND tgname = 'retain_project_rollback_preparation'
    ) THEN
        CREATE TRIGGER retain_project_rollback_preparation
            BEFORE INSERT OR UPDATE ON deployment_rollback_operations
            FOR EACH ROW EXECUTE FUNCTION retain_project_rollback_preparation();
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION retain_project_rollback_phase() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.receipt ? 'phase' AND NOT (NEW.receipt ? 'phase') THEN
        NEW.receipt := NEW.receipt || jsonb_build_object(
            'phase', OLD.receipt->'phase', 'preparation_deadline', OLD.receipt->'preparation_deadline');
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgrelid = 'project_rollback_operations'::regclass AND tgname = 'retain_project_rollback_phase'
    ) THEN
        CREATE TRIGGER retain_project_rollback_phase BEFORE UPDATE ON project_rollback_operations
            FOR EACH ROW EXECUTE FUNCTION retain_project_rollback_phase();
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_project_rollback_preparation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'live' AND NEW.traffic_percent > (CASE WHEN OLD.status = 'live' THEN OLD.traffic_percent ELSE 0 END)
       AND EXISTS (
           SELECT 1 FROM deployment_rollback_operations r
           WHERE r.app_id = NEW.app_id AND r.target_deployment_id = NEW.id AND r.project_rollback_id IS NOT NULL
             AND r.status NOT IN ('complete', 'failed')
             AND r.id::text IS DISTINCT FROM current_setting('faas.project_rollback_cutover', true)
       ) THEN
        RAISE EXCEPTION 'project rollback preparation has not released this target'
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
        WHERE tgrelid = 'deployments'::regclass AND tgname = 'guard_project_rollback_preparation'
    ) THEN
        CREATE TRIGGER guard_project_rollback_preparation
            BEFORE UPDATE OF status,traffic_percent ON deployments
            FOR EACH ROW EXECUTE FUNCTION guard_project_rollback_preparation();
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER guard_project_rollback_preparation ON deployments;
DROP FUNCTION guard_project_rollback_preparation();
DROP TRIGGER retain_project_rollback_phase ON project_rollback_operations;
DROP FUNCTION retain_project_rollback_phase();
DROP TRIGGER retain_project_rollback_preparation ON deployment_rollback_operations;
DROP FUNCTION retain_project_rollback_preparation();
ALTER TABLE deployment_rollback_operations DROP COLUMN project_rollback_id;
