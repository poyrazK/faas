-- filename: 20261004190339147_service_binding_dependency_revision.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_binding_promotion_revisions ADD COLUMN IF NOT EXISTS service_revision bigint NOT NULL DEFAULT 1 CHECK (service_revision > 0);
-- Reinstallation must not reuse tokens issued before a rollback.
UPDATE app_binding_promotion_revisions SET epoch=gen_random_uuid(),revision=revision+1
 WHERE app_id IN (SELECT id FROM apps WHERE COALESCE(manifest->'service_bindings','[]'::jsonb)<>'[]'::jsonb);

-- A logical name may resolve to production, a project PR sibling or a test-run
-- workload. Account-wide invalidation avoids duplicating the gateway resolver.
-- Probe completion and runtime heartbeats never increment this revision.
CREATE OR REPLACE FUNCTION capture_service_binding_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 old_row jsonb := CASE WHEN TG_OP='INSERT' THEN '{}'::jsonb ELSE to_jsonb(OLD) END;
 new_row jsonb := CASE WHEN TG_OP='DELETE' THEN '{}'::jsonb ELSE to_jsonb(NEW) END;
 old_facts jsonb;
 new_facts jsonb;
 target uuid;
 watched text[] := string_to_array(TG_ARGV[0],',');
BEGIN
 IF TG_OP='UPDATE' THEN
  SELECT jsonb_object_agg(key,value) INTO old_facts FROM jsonb_each(old_row) WHERE key=ANY(watched);
  SELECT jsonb_object_agg(key,value) INTO new_facts FROM jsonb_each(new_row) WHERE key=ANY(watched);
  IF old_facts IS NOT DISTINCT FROM new_facts THEN RETURN NEW; END IF;
 END IF;
 FOR target IN SELECT a.id FROM apps a
  WHERE a.account_id IN (NULLIF(old_row->>'account_id','')::uuid,NULLIF(new_row->>'account_id','')::uuid)
   AND a.status<>'deleted' AND COALESCE(a.manifest->'service_bindings','[]'::jsonb)<>'[]'::jsonb
  ORDER BY a.id LOOP
  INSERT INTO app_binding_promotion_revisions(app_id,service_revision) VALUES(target,2)
  ON CONFLICT(app_id) DO UPDATE SET
   revision=app_binding_promotion_revisions.revision+1,
   service_revision=app_binding_promotion_revisions.service_revision+1;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $$;
CREATE OR REPLACE TRIGGER service_binding_revision AFTER INSERT OR UPDATE OR DELETE ON apps
 FOR EACH ROW EXECUTE FUNCTION capture_service_binding_revision('id,account_id,slug,status,manifest,project_id,preview_of_slug,preview_pr_number,preview_pr_state,preview_expires_at,app_protocol,websocket_enabled');
CREATE OR REPLACE TRIGGER service_binding_revision AFTER INSERT OR UPDATE OR DELETE ON github_deploy_policies
 FOR EACH ROW EXECUTE FUNCTION capture_service_binding_revision('project_id,account_id,preview_service_policy');
CREATE OR REPLACE TRIGGER service_binding_revision AFTER INSERT OR UPDATE OR DELETE ON scenario_test_members
 FOR EACH ROW EXECUTE FUNCTION capture_service_binding_revision('account_id,run_id,workload_name,app_id');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER service_binding_revision ON scenario_test_members;
DROP TRIGGER service_binding_revision ON github_deploy_policies;
DROP TRIGGER service_binding_revision ON apps;
DROP FUNCTION capture_service_binding_revision();
ALTER TABLE app_binding_promotion_revisions DROP COLUMN service_revision;
-- +goose StatementEnd
