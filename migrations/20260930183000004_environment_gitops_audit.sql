-- +goose Up
CREATE TABLE IF NOT EXISTS environment_gitops_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id uuid NOT NULL REFERENCES environment_git_sources(id) ON DELETE CASCADE,
    actor text NOT NULL CHECK (actor <> ''),
    kind text NOT NULL CHECK (kind IN ('adopt', 'control', 'override_created', 'override_removed')),
    details jsonb NOT NULL CHECK (jsonb_typeof(details) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS environment_gitops_events_history ON environment_gitops_events(source_id, created_at DESC, id DESC);

-- Prevent changing a managed row's identity to escape the scoped value guard.
-- Normal APIs replace values or delete rows; they never move their identities.
-- +goose StatementBegin
DO $replay$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE p.proname='environment_gitops_guard_identity' AND n.nspname=current_schema()) THEN
    EXECUTE $function$CREATE FUNCTION environment_gitops_guard_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (to_jsonb(NEW) - ARRAY['value', 'updated_at', 'only_allow_declared_routes', 'declared_routes', 'rules'])
       IS DISTINCT FROM (to_jsonb(OLD) - ARRAY['value', 'updated_at', 'only_allow_declared_routes', 'declared_routes', 'rules'])
       AND EXISTS (SELECT 1 FROM environment_gitops_resources r JOIN environment_git_sources s ON s.id = r.source_id
           JOIN project_environments e ON e.id = s.environment_id
           WHERE r.app_id = OLD.app_id AND s.account_id = OLD.account_id
           AND e.slug = coalesce(to_jsonb(OLD)->>'scope', to_jsonb(OLD)->>'environment_slug')) THEN
        RAISE EXCEPTION 'environment resource identity cannot be moved'
            USING ERRCODE = '23514', CONSTRAINT = 'environment_gitops_field_owned';
    END IF;
    RETURN NEW;
END $$;$function$;
  END IF;
END $replay$;
DROP TRIGGER IF EXISTS environment_gitops_guard_identity_env ON app_envs;
CREATE TRIGGER environment_gitops_guard_identity_env BEFORE UPDATE ON app_envs
FOR EACH ROW EXECUTE FUNCTION environment_gitops_guard_identity();
DROP TRIGGER IF EXISTS environment_gitops_guard_identity_routes ON project_environment_route_policies;
CREATE TRIGGER environment_gitops_guard_identity_routes BEFORE UPDATE ON project_environment_route_policies
FOR EACH ROW EXECUTE FUNCTION environment_gitops_guard_identity();
DROP TRIGGER IF EXISTS environment_gitops_guard_identity_policies ON project_environment_edge_policies;
CREATE TRIGGER environment_gitops_guard_identity_policies BEFORE UPDATE ON project_environment_edge_policies
FOR EACH ROW EXECUTE FUNCTION environment_gitops_guard_identity();
-- +goose StatementEnd

-- +goose Down
-- Preserve durable ownership, accepted work and runtime evidence on rollback.
SELECT 1;
