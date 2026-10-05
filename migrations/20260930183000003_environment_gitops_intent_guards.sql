-- +goose Up
-- +goose StatementBegin
-- Every scoped writer (API, Terraform, clone/promotion, or SQL tooling) enters
-- the same ownership gate. Report mode permits changes and schedules a check.
-- Enforce mode requires a current controller lease or an unexpired override.
DO $replay$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE p.proname='environment_gitops_guard_intent' AND n.nspname=current_schema()) THEN
    EXECUTE $function$CREATE FUNCTION environment_gitops_guard_intent() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    row_value jsonb;
    src environment_git_sources%ROWTYPE;
    resource_name text;
    paths text[];
    prior_config jsonb;
    controller boolean;
BEGIN
    IF TG_OP = 'DELETE' THEN row_value := to_jsonb(OLD); ELSE row_value := to_jsonb(NEW); END IF;
    IF TG_TABLE_NAME = 'project_environment_config_versions' THEN
        SELECT s.* INTO src FROM environment_git_sources s JOIN project_environments e ON e.id = s.environment_id
        WHERE s.account_id = (row_value->>'account_id')::uuid AND s.project_id = (row_value->>'project_id')::uuid
          AND e.slug = row_value->>'environment_slug' FOR UPDATE OF s;
        resource_name := 'environment';
        SELECT config_json INTO prior_config FROM project_environment_config_versions
        WHERE project_id = (row_value->>'project_id')::uuid AND environment_slug = row_value->>'environment_slug'
        ORDER BY version DESC LIMIT 1;
        SELECT array_agg('configuration/' || k) INTO paths FROM (
            SELECT key AS k FROM jsonb_each(coalesce(prior_config, '{}'))
            UNION SELECT key FROM jsonb_each(coalesce(row_value->'config_json', '{}'))
        ) keys WHERE (prior_config->k) IS DISTINCT FROM (row_value->'config_json'->k);
    ELSE
        SELECT s.* INTO src
        FROM environment_git_sources s JOIN project_environments e ON e.id = s.environment_id
        JOIN environment_gitops_resources r ON r.source_id = s.id
        WHERE s.account_id = (row_value->>'account_id')::uuid AND r.app_id = (row_value->>'app_id')::uuid
          AND e.slug = coalesce(row_value->>'scope', row_value->>'environment_slug') FOR UPDATE OF s;
        SELECT logical_name INTO resource_name FROM environment_gitops_resources
        WHERE source_id = src.id AND app_id = (row_value->>'app_id')::uuid;
        IF TG_TABLE_NAME = 'app_envs' THEN paths := ARRAY['variables/' || (row_value->>'key')];
        ELSIF TG_TABLE_NAME = 'project_environment_route_policies' THEN paths := ARRAY['routes'];
        ELSE paths := ARRAY['policies']; END IF;
    END IF;
    IF src.id IS NOT NULL THEN
        controller := EXISTS (SELECT 1 FROM environment_gitops_jobs j
            WHERE j.source_id = src.id AND j.desired_generation = src.generation AND j.claimed_generation = src.generation
              AND j.lease_until > clock_timestamp() AND j.lease_token <> ''
              AND j.lease_token = current_setting('gregale.gitops_lease', true)
              AND src.approved_revision_id IS NOT NULL AND NOT src.suspended);
        IF src.mode = 'enforce' AND NOT controller AND EXISTS (
            SELECT 1 FROM environment_managed_fields f
            WHERE f.environment_id = src.environment_id AND f.resource = resource_name AND f.field_path = ANY(paths)
              AND f.source_id = src.id AND NOT EXISTS (
                  SELECT 1 FROM environment_management_overrides o
                  WHERE o.environment_id = f.environment_id AND o.resource = f.resource AND o.field_path = f.field_path
                    AND o.expires_at > clock_timestamp())) THEN
            RAISE EXCEPTION 'setting is managed by the environment Git source'
                USING ERRCODE = '23514', CONSTRAINT = 'environment_gitops_field_owned';
        END IF;
        UPDATE environment_git_sources SET intent_version = intent_version + 1, updated_at = now() WHERE id = src.id;
        IF src.generation > 0 THEN
            INSERT INTO environment_gitops_jobs(source_id, desired_generation, next_attempt_at)
            VALUES (src.id, src.generation, now()) ON CONFLICT (source_id) DO UPDATE
            SET next_attempt_at = least(environment_gitops_jobs.next_attempt_at, excluded.next_attempt_at);
        END IF;
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;$function$;
  END IF;
END $replay$;

DROP TRIGGER IF EXISTS environment_gitops_guard_env ON app_envs;
CREATE TRIGGER environment_gitops_guard_env BEFORE INSERT OR UPDATE OR DELETE ON app_envs
FOR EACH ROW EXECUTE FUNCTION environment_gitops_guard_intent();
DROP TRIGGER IF EXISTS environment_gitops_guard_routes ON project_environment_route_policies;
CREATE TRIGGER environment_gitops_guard_routes BEFORE INSERT OR UPDATE OR DELETE ON project_environment_route_policies
FOR EACH ROW EXECUTE FUNCTION environment_gitops_guard_intent();
DROP TRIGGER IF EXISTS environment_gitops_guard_policies ON project_environment_edge_policies;
CREATE TRIGGER environment_gitops_guard_policies BEFORE INSERT OR UPDATE OR DELETE ON project_environment_edge_policies
FOR EACH ROW EXECUTE FUNCTION environment_gitops_guard_intent();
DROP TRIGGER IF EXISTS environment_gitops_guard_config ON project_environment_config_versions;
CREATE TRIGGER environment_gitops_guard_config BEFORE INSERT ON project_environment_config_versions
FOR EACH ROW EXECUTE FUNCTION environment_gitops_guard_intent();
-- +goose StatementEnd

-- +goose Down
-- Preserve durable ownership, accepted work and runtime evidence on rollback.
SELECT 1;
