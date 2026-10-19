-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS project_release_secret_bundles (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 request_id text NOT NULL CHECK(request_id ~ '^[a-f0-9]{32}$'),
 request_hash text NOT NULL CHECK(request_hash ~ '^[a-f0-9]{64}$'),
 values_json jsonb NOT NULL CHECK(jsonb_typeof(values_json)='array'),
 response_json jsonb NOT NULL CHECK(jsonb_typeof(response_json)='object'),
 created_at timestamptz NOT NULL DEFAULT now() CHECK(isfinite(created_at)),
 PRIMARY KEY(account_id,request_id)
);
CREATE INDEX IF NOT EXISTS project_release_secret_bundles_project_created ON project_release_secret_bundles(project_id,created_at);
CREATE TABLE IF NOT EXISTS deployment_release_secret_snapshots (
 deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
 values_json jsonb NOT NULL CHECK(jsonb_typeof(values_json)='array'),
 created_at timestamptz NOT NULL DEFAULT now() CHECK(isfinite(created_at))
);
-- Immutable logical versions; key reseals require a separate qualified workflow.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reject_pinned_release_secret_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE selected_app uuid; selected_scope text;
BEGIN
 selected_app := COALESCE(NEW.app_id,OLD.app_id);
 selected_scope := COALESCE(NEW.scope,OLD.scope);
 -- Serialize with configuration staging and graph publication, before deciding
 -- whether a mutable operation is admissible.
 PERFORM 1 FROM apps WHERE id=selected_app AND status<>'deleted' FOR UPDATE;
 IF EXISTS (SELECT 1 FROM project_release_sets r JOIN project_release_members m ON m.release_id=r.id
  JOIN deployment_release_secret_snapshots s ON s.deployment_id=m.deployment_id JOIN apps a ON a.id=m.app_id AND a.status<>'deleted'
  WHERE r.active AND r.environment_slug=selected_scope AND m.app_id=selected_app) THEN
  -- Delivery observations do not alter sealed configuration.
  IF TG_OP='UPDATE' AND NEW.ciphertext=OLD.ciphertext AND NEW.kid IS NOT DISTINCT FROM OLD.kid
   AND NEW.value_hash IS NOT DISTINCT FROM OLD.value_hash AND NEW.secret_version IS NOT DISTINCT FROM OLD.secret_version
   AND NEW.delivery_version=OLD.delivery_version AND NEW.app_id=OLD.app_id AND NEW.account_id=OLD.account_id AND NEW.scope=OLD.scope AND NEW.key=OLD.key
   AND NEW.managed_credential_ref IS NOT DISTINCT FROM OLD.managed_credential_ref AND NEW.managed_credential_generation IS NOT DISTINCT FROM OLD.managed_credential_generation
   AND NEW.secret_class=OLD.secret_class AND NEW.managed_postgres_binding_id IS NOT DISTINCT FROM OLD.managed_postgres_binding_id
   AND NEW.managed_object_storage_credential_id IS NOT DISTINCT FROM OLD.managed_object_storage_credential_id THEN RETURN NEW; END IF;
  RAISE EXCEPTION 'release secrets require an atomic secret deployment' USING ERRCODE='23514',CONSTRAINT='release_secret_snapshot_owned';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER release_secret_snapshot_owned BEFORE INSERT OR UPDATE OR DELETE ON app_secrets
 FOR EACH ROW EXECUTE FUNCTION reject_pinned_release_secret_mutation();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM deployment_release_secret_snapshots) THEN RAISE EXCEPTION 'cannot discard release secret snapshots'; END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER release_secret_snapshot_owned ON app_secrets;
DROP FUNCTION reject_pinned_release_secret_mutation();
DROP TABLE deployment_release_secret_snapshots;
DROP TABLE project_release_secret_bundles;
