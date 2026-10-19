-- filename: 20261003212646000_application_standard_source_rootfs_fences.sql
-- adr: 435. Freeze current build claims; permit only eligible claimed owner erasure.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION source_build_rootfs_owner_erasing(deployment uuid) RETURNS boolean LANGUAGE sql VOLATILE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM deployments WHERE id=deployment)
 OR EXISTS(SELECT 1 FROM deployments d JOIN apps a ON a.id=d.app_id
  WHERE d.id=deployment AND a.status='deleted' AND a.delete_grace_until<=clock_timestamp() AND a.purge_claimed_at IS NOT NULL);
$$;

CREATE OR REPLACE FUNCTION lock_source_build_rootfs(input jsonb) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE p build_export_publications%ROWTYPE; a apps%ROWTYPE; d deployments%ROWTYPE; b builds%ROWTYPE;
BEGIN
 SELECT * INTO p FROM build_export_publications WHERE id=(input->>'publication_id')::uuid FOR SHARE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'source approval missing' USING ERRCODE='23514',CONSTRAINT='build_export_publication_missing';END IF;
 SELECT * INTO d FROM deployments WHERE id=p.deployment_id FOR UPDATE NOWAIT;
 SELECT * INTO a FROM apps WHERE id=p.app_id FOR SHARE NOWAIT;
 -- Lock queued claims too. Deployment FOR UPDATE blocks new FK children.
 PERFORM id FROM builds WHERE deployment_id=d.id ORDER BY id FOR SHARE NOWAIT;
 SELECT * INTO b FROM builds WHERE deployment_id=d.id ORDER BY started_at DESC NULLS LAST,id DESC LIMIT 1 FOR SHARE NOWAIT;
 IF (d.app_id=a.id AND a.account_id=p.account_id AND a.status<>'deleted'
  AND p.build_id=b.id AND d.build_id=p.build_id AND p.deployment_id::text=input->>'deployment_id' AND p.app_id::text=input->>'app_id'
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

CREATE OR REPLACE FUNCTION source_build_rootfs_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND current_setting('gregale.source_build_rootfs_insert',true)=NEW.id::text THEN RETURN NEW;END IF;
 IF TG_OP='DELETE' AND source_build_rootfs_owner_erasing(OLD.deployment_id) THEN RETURN OLD;END IF;
 RAISE EXCEPTION 'source rootfs evidence is immutable/private' USING ERRCODE='23514',CONSTRAINT='build_export_publication_immutable';
END;
$$;

CREATE OR REPLACE FUNCTION source_build_rootfs_current_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' AND source_build_rootfs_owner_erasing(OLD.deployment_id) THEN RETURN OLD;END IF;
 IF TG_OP<>'DELETE' AND current_setting('gregale.source_build_rootfs_insert',true)=NEW.artifact_id::text
  AND (TG_OP='INSERT' OR NEW.deployment_id=OLD.deployment_id) THEN RETURN NEW;END IF;
 RAISE EXCEPTION 'source rootfs selection must use private publication' USING ERRCODE='23514',CONSTRAINT='build_export_publication_immutable';
END;
$$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lock_source_build_rootfs(input jsonb) RETURNS jsonb LANGUAGE plpgsql AS $$
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

CREATE OR REPLACE FUNCTION source_build_rootfs_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND current_setting('gregale.source_build_rootfs_insert',true)=NEW.id::text THEN RETURN NEW;END IF;
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN RETURN OLD;END IF;
 RAISE EXCEPTION 'source rootfs evidence is immutable/private' USING ERRCODE='23514',CONSTRAINT='build_export_publication_immutable';
END;
$$;

CREATE OR REPLACE FUNCTION source_build_rootfs_current_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN RETURN OLD;END IF;
 IF TG_OP<>'DELETE' AND current_setting('gregale.source_build_rootfs_insert',true)=NEW.artifact_id::text
  AND (TG_OP='INSERT' OR NEW.deployment_id=OLD.deployment_id) THEN RETURN NEW;END IF;
 RAISE EXCEPTION 'source rootfs selection must use private publication' USING ERRCODE='23514',CONSTRAINT='build_export_publication_immutable';
END;
$$;
DROP FUNCTION source_build_rootfs_owner_erasing(uuid);
-- +goose StatementEnd
