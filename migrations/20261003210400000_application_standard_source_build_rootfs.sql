-- filename: 20261003210400000_application_standard_source_build_rootfs.sql
-- adr: 435. Private source conversion evidence; no scan/native/adoption authority.
-- +goose Up
-- +goose StatementBegin
CREATE INDEX build_export_publications_scope ON build_export_publications(account_id,app_id,deployment_id);
CREATE TABLE source_build_rootfs (
 id uuid PRIMARY KEY,
 publication_id uuid NOT NULL REFERENCES build_export_publications(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 input_snapshot jsonb NOT NULL CHECK(jsonb_typeof(input_snapshot)='object'),
 input_hash text NOT NULL CHECK(input_hash ~ '^[a-f0-9]{64}$'),
 published_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>published_at AND expires_at<=published_at+interval '24 hours'),
 UNIQUE(id,deployment_id),
 CHECK((input_snapshot->>'publication_id'=publication_id::text AND input_snapshot->>'deployment_id'=deployment_id::text
  AND input_snapshot->>'account_id' ~ '^[a-f0-9-]{36}$' AND input_snapshot->>'app_id' ~ '^[a-f0-9-]{36}$'
  AND input_snapshot->>'publication_hash' ~ '^[a-f0-9]{64}$' AND input_snapshot->>'intent_hash' ~ '^[a-f0-9]{64}$'
  AND input_snapshot->>'base_producer_id' ~ '^[a-f0-9-]{36}$' AND input_snapshot->>'base_input_hash' ~ '^[a-f0-9]{64}$'
  AND input_snapshot->>'artifact_digest' ~ '^sha256:[a-f0-9]{64}$' AND (input_snapshot->>'artifact_bytes')::bigint>0
  AND (input_snapshot->>'content_bytes')::bigint>=0 AND coalesce(input_snapshot->>'storage_key','')<>''
  AND coalesce(input_snapshot->>'rootfs_path','')<>'' AND input_snapshot->>'guest_init_digest' ~ '^sha256:[a-f0-9]{64}$'
  AND input_snapshot->>'layout_version'='faas-app-layer-layout-v1'
  AND ((input_snapshot->>'kind'='source-app-layer' AND input_snapshot->>'runtime'='' AND input_snapshot->>'runner_digest'='')
   OR (input_snapshot->>'kind'='function-layer' AND coalesce(input_snapshot->>'runtime','')<>''
    AND input_snapshot->>'runner_digest' ~ '^sha256:[a-f0-9]{64}$'))) IS TRUE)
);
CREATE TABLE source_build_rootfs_current (
 deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
 artifact_id uuid NOT NULL,
 FOREIGN KEY(artifact_id,deployment_id) REFERENCES source_build_rootfs(id,deployment_id) ON DELETE CASCADE
);

CREATE FUNCTION source_build_rootfs_intent(a public.apps,d public.deployments) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
 SELECT jsonb_build_object('slug',a.slug,'type',a.type,'runtime',coalesce(a.runtime,''),'start_command',coalesce(a.start_command,''),
 'manifest',a.manifest,'require_signed',a.require_signed,'security_policy',a.security_policy,'handler',coalesce(d.handler,''),
 'scope',d.scope,'override_entrypoint',d.override_entrypoint,'override_cmd',d.override_cmd,'override_env',d.override_env,
 'override_env_secrets',d.override_env_secrets,'override_port',coalesce(d.override_port,0),'override_healthcheck',d.override_healthcheck,
 'override_liveness_probe',d.override_liveness_probe,'override_readiness_probe',d.override_readiness_probe,
 'override_main_depends_on',d.override_main_depends_on);
$$;

CREATE FUNCTION lock_source_build_rootfs(input jsonb) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE p build_export_publications%ROWTYPE; a apps%ROWTYPE; d deployments%ROWTYPE; b builds%ROWTYPE;
BEGIN
 SELECT * INTO p FROM build_export_publications WHERE id=(input->>'publication_id')::uuid FOR SHARE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'source approval missing' USING ERRCODE='23514',CONSTRAINT='build_export_publication_missing';END IF;
 SELECT * INTO d FROM deployments WHERE id=p.deployment_id FOR UPDATE NOWAIT;
 SELECT * INTO a FROM apps WHERE id=p.app_id FOR SHARE NOWAIT;
 SELECT * INTO b FROM builds WHERE deployment_id=d.id ORDER BY started_at DESC NULLS LAST,id DESC LIMIT 1 FOR SHARE NOWAIT;
 IF (d.app_id=a.id AND a.account_id=p.account_id AND a.status<>'deleted'
  AND p.build_id=b.id AND p.deployment_id::text=input->>'deployment_id' AND p.app_id::text=input->>'app_id'
  AND p.account_id::text=input->>'account_id' AND p.input_hash=input->>'publication_hash'
  AND p.verified_at<=clock_timestamp() AND p.expires_at>clock_timestamp()
  AND d.status IN ('pending','building','imaging','snapshotting')) IS NOT TRUE THEN
  RAISE EXCEPTION 'source conversion inputs changed' USING ERRCODE='23514',CONSTRAINT='build_export_publication_stale';
 END IF;
 RETURN jsonb_build_object('intent',source_build_rootfs_intent(a,d),'status',d.status,'checked_at',clock_timestamp());
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'source conversion inputs busy' USING ERRCODE='55P03',CONSTRAINT='build_export_publication_busy';
END;
$$;

CREATE FUNCTION source_build_rootfs_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND current_setting('gregale.source_build_rootfs_insert',true)=NEW.id::text THEN RETURN NEW;END IF;
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN RETURN OLD;END IF;
 RAISE EXCEPTION 'source rootfs evidence is immutable/private' USING ERRCODE='23514',CONSTRAINT='build_export_publication_immutable';
END;
$$;
CREATE FUNCTION source_build_rootfs_current_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN RETURN OLD;END IF;
 IF TG_OP<>'DELETE' AND current_setting('gregale.source_build_rootfs_insert',true)=NEW.artifact_id::text
  AND (TG_OP='INSERT' OR NEW.deployment_id=OLD.deployment_id) THEN RETURN NEW;END IF;
 RAISE EXCEPTION 'source rootfs selection must use private publication' USING ERRCODE='23514',CONSTRAINT='build_export_publication_immutable';
END;
$$;
CREATE TRIGGER source_build_rootfs_private BEFORE INSERT OR UPDATE OR DELETE ON source_build_rootfs
 FOR EACH ROW EXECUTE FUNCTION source_build_rootfs_guard();
CREATE TRIGGER source_build_rootfs_current_private BEFORE INSERT OR UPDATE OR DELETE ON source_build_rootfs_current
 FOR EACH ROW EXECUTE FUNCTION source_build_rootfs_current_guard();
CREATE TRIGGER application_standard_source_rootfs_child BEFORE INSERT OR UPDATE OR DELETE ON source_build_rootfs
 FOR EACH ROW EXECUTE FUNCTION application_standard_artifact_child_guard();
CREATE TRIGGER application_standard_source_rootfs_current_child BEFORE INSERT OR UPDATE OR DELETE ON source_build_rootfs_current
 FOR EACH ROW EXECUTE FUNCTION application_standard_artifact_child_guard();
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE source_build_rootfs_current;
DROP TABLE source_build_rootfs;
DROP FUNCTION source_build_rootfs_current_guard();
DROP FUNCTION source_build_rootfs_guard();
DROP FUNCTION lock_source_build_rootfs(jsonb);
DROP FUNCTION source_build_rootfs_intent(public.apps,public.deployments);
DROP INDEX build_export_publications_scope;
-- +goose StatementEnd
