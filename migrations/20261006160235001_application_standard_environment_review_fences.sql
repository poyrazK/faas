-- +goose Up
-- Exact-plan approval takes an exclusive per-application advisory fence.
-- Environment writers take its shared side, including first-head insertion
-- and environment lifetime changes. No additional parent-row lock is added
-- to writers whose existing deployment/app lock order differs.
-- +goose StatementBegin
CREATE FUNCTION application_standard_environment_workload_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app_ids uuid[] := '{}'; project_ids uuid[] := '{}'; deployment_ids uuid[] := '{}';
BEGIN
    IF TG_TABLE_NAME = 'project_environments' THEN
        IF TG_OP <> 'INSERT' THEN project_ids := array_append(project_ids, OLD.project_id); END IF;
        IF TG_OP <> 'DELETE' THEN project_ids := array_append(project_ids, NEW.project_id); END IF;
        SELECT coalesce(array_agg(id), '{}') INTO app_ids FROM apps WHERE project_id = ANY(project_ids);
    ELSIF TG_TABLE_NAME = 'project_environment_workload_deployment_specs' THEN
        IF TG_OP <> 'INSERT' THEN deployment_ids := array_append(deployment_ids, OLD.deployment_id); END IF;
        IF TG_OP <> 'DELETE' THEN deployment_ids := array_append(deployment_ids, NEW.deployment_id); END IF;
        SELECT coalesce(array_agg(app_id), '{}') INTO app_ids FROM deployments WHERE id = ANY(deployment_ids);
    ELSE
        IF TG_OP <> 'INSERT' THEN app_ids := array_append(app_ids, OLD.app_id); END IF;
        IF TG_OP <> 'DELETE' THEN app_ids := array_append(app_ids, NEW.app_id); END IF;
    END IF;
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.environment-workloads.' || id::text, 0))
    FROM (SELECT DISTINCT id FROM unnest(app_ids) id WHERE id IS NOT NULL ORDER BY id) owners;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER application_standard_environment_workload_guard
BEFORE INSERT OR UPDATE OR DELETE ON project_environment_workload_specs
FOR EACH ROW EXECUTE FUNCTION application_standard_environment_workload_guard();
CREATE TRIGGER application_standard_environment_workload_head_guard
BEFORE INSERT OR UPDATE OR DELETE ON project_environment_workload_heads
FOR EACH ROW EXECUTE FUNCTION application_standard_environment_workload_guard();
CREATE TRIGGER application_standard_environment_workload_pin_guard
BEFORE INSERT OR UPDATE OR DELETE ON project_environment_workload_deployment_specs
FOR EACH ROW EXECUTE FUNCTION application_standard_environment_workload_guard();
CREATE TRIGGER application_standard_environment_lifetime_guard
BEFORE INSERT OR UPDATE OR DELETE ON project_environments
FOR EACH ROW EXECUTE FUNCTION application_standard_environment_workload_guard();

-- +goose Down
DROP TRIGGER application_standard_environment_lifetime_guard ON project_environments;
DROP TRIGGER application_standard_environment_workload_pin_guard ON project_environment_workload_deployment_specs;
DROP TRIGGER application_standard_environment_workload_head_guard ON project_environment_workload_heads;
DROP TRIGGER application_standard_environment_workload_guard ON project_environment_workload_specs;
DROP FUNCTION application_standard_environment_workload_guard();
