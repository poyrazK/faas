-- +goose Up
-- +goose StatementBegin
DO $replay$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE p.proname='environment_gitops_guard_app_presence' AND n.nspname=current_schema()) THEN
    EXECUTE $function$CREATE FUNCTION environment_gitops_guard_app_presence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE src environment_git_sources%ROWTYPE;
BEGIN
    IF pg_trigger_depth() > 1 THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.status = OLD.status THEN RETURN NEW; END IF;
    FOR src IN SELECT s.* FROM environment_git_sources s
        JOIN environment_gitops_resources r ON r.source_id = s.id
        WHERE r.app_id = OLD.id ORDER BY s.id FOR UPDATE OF s
    LOOP
        IF src.mode = 'enforce' AND EXISTS (
            SELECT 1 FROM environment_managed_fields f JOIN environment_gitops_resources r
            ON r.source_id = f.source_id AND r.logical_name = f.resource
            WHERE f.source_id = src.id AND r.app_id = OLD.id AND f.field_path = 'presence'
              AND NOT EXISTS (SELECT 1 FROM environment_management_overrides o
                  WHERE o.environment_id = f.environment_id AND o.resource = f.resource AND o.field_path = 'presence'
                    AND o.expires_at > clock_timestamp())) THEN
            RAISE EXCEPTION 'workload membership is managed by the environment Git source'
                USING ERRCODE = '23514', CONSTRAINT = 'environment_gitops_field_owned';
        END IF;
        UPDATE environment_git_sources SET intent_version = intent_version + 1, updated_at = now() WHERE id = src.id;
        IF src.generation > 0 THEN
            INSERT INTO environment_gitops_jobs(source_id, desired_generation, next_attempt_at)
            VALUES (src.id, src.generation, now()) ON CONFLICT (source_id) DO UPDATE
            SET next_attempt_at = least(environment_gitops_jobs.next_attempt_at, excluded.next_attempt_at);
        END IF;
    END LOOP;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;$function$;
  END IF;
END $replay$;
DROP TRIGGER IF EXISTS environment_gitops_guard_app_presence ON apps;
CREATE TRIGGER environment_gitops_guard_app_presence BEFORE UPDATE OF status OR DELETE ON apps
FOR EACH ROW EXECUTE FUNCTION environment_gitops_guard_app_presence();

-- Configuration versions are append-only while managed. Deleting the parent
-- environment/project still cascades normally through its existing cleanup.
DO $replay$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE p.proname='environment_gitops_guard_config_history' AND n.nspname=current_schema()) THEN
    EXECUTE $function$CREATE FUNCTION environment_gitops_guard_config_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF pg_trigger_depth() <= 1 AND EXISTS (
        SELECT 1 FROM environment_git_sources s JOIN project_environments e ON e.id = s.environment_id
        WHERE s.project_id = OLD.project_id AND s.account_id = OLD.account_id AND e.slug = OLD.environment_slug) THEN
        RAISE EXCEPTION 'managed configuration history is append-only'
            USING ERRCODE = '23514', CONSTRAINT = 'environment_gitops_field_owned';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;$function$;
  END IF;
END $replay$;
DROP TRIGGER IF EXISTS environment_gitops_guard_config_history ON project_environment_config_versions;
CREATE TRIGGER environment_gitops_guard_config_history BEFORE UPDATE OR DELETE ON project_environment_config_versions
FOR EACH ROW EXECUTE FUNCTION environment_gitops_guard_config_history();
-- +goose StatementEnd

-- +goose Down
-- Preserve durable ownership, accepted work and runtime evidence on rollback.
SELECT 1;
