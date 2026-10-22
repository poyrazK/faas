-- +goose Up
-- adr: 387. Producer lineage/bytes, not scan or native/consumer authority.
CREATE TABLE deployment_registry_rootfs (
 id uuid PRIMARY KEY,
 registry_verification_id uuid NOT NULL REFERENCES deployment_registry_verifications(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 workload_name text NOT NULL CHECK (workload_name='' OR (workload_name <> 'main' AND workload_name ~ '^[a-z0-9][a-z0-9-]{0,62}$')),
 input_snapshot jsonb NOT NULL CHECK (jsonb_typeof(input_snapshot)='object'),
 input_hash text NOT NULL CHECK (input_hash ~ '^[a-f0-9]{64}$'),
 published_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK (expires_at>published_at AND expires_at<=published_at+interval '24 hours'),
 UNIQUE(id,deployment_id,workload_name),
 CHECK (input_snapshot->>'deployment_id'=deployment_id::text AND input_snapshot->>'workload_name'=workload_name
  AND input_snapshot->>'registry_verification_id'=registry_verification_id::text)
);
CREATE TABLE deployment_registry_rootfs_current (
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 workload_name text NOT NULL CHECK (workload_name='' OR (workload_name <> 'main' AND workload_name ~ '^[a-z0-9][a-z0-9-]{0,62}$')),
 artifact_id uuid NOT NULL,
 PRIMARY KEY(deployment_id,workload_name),
 FOREIGN KEY(artifact_id,deployment_id,workload_name) REFERENCES deployment_registry_rootfs(id,deployment_id,workload_name) ON DELETE CASCADE
);
-- +goose StatementBegin
CREATE FUNCTION lock_deployment_registry_rootfs(verification_id uuid) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE r deployment_registry_verifications%ROWTYPE; d deployments%ROWTYPE; owner_inputs jsonb;
BEGIN
 SELECT * INTO r FROM deployment_registry_verifications WHERE id=verification_id;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'registry verification missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing';
 END IF;
 SELECT * INTO d FROM deployments WHERE id=r.deployment_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR d.status NOT IN ('pending','building','imaging','snapshotting') THEN
  RAISE EXCEPTION 'registry conversion is no longer active' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_stale';
 END IF;
 owner_inputs:=lock_deployment_registry_verification(r.app_id,r.deployment_id,r.account_id,r.workload_name,r.input_snapshot->'proof'->>'PublisherName');
 RETURN owner_inputs || jsonb_build_object('scope',d.scope,'status',d.status,'storage_now',clock_timestamp());
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'registry rootfs inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION deployment_registry_rootfs_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND current_setting('gregale.registry_rootfs_insert',true)=NEW.id::text THEN RETURN NEW; END IF;
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'registry rootfs evidence is immutable/private' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_immutable';
END;
$$;
CREATE FUNCTION deployment_registry_rootfs_current_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN RETURN OLD; END IF;
 IF TG_OP <> 'DELETE' AND current_setting('gregale.registry_rootfs_insert',true)=NEW.artifact_id::text
   AND (TG_OP='INSERT' OR (NEW.deployment_id=OLD.deployment_id AND NEW.workload_name=OLD.workload_name)) THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'registry rootfs selection must use private publication' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_immutable';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER registry_rootfs_private_guard BEFORE INSERT OR UPDATE OR DELETE ON deployment_registry_rootfs
 FOR EACH ROW EXECUTE FUNCTION deployment_registry_rootfs_guard();
CREATE TRIGGER registry_rootfs_current_private_guard BEFORE INSERT OR UPDATE OR DELETE ON deployment_registry_rootfs_current
 FOR EACH ROW EXECUTE FUNCTION deployment_registry_rootfs_current_guard();
CREATE TRIGGER application_standard_registry_rootfs_child_guard BEFORE INSERT OR UPDATE OR DELETE ON deployment_registry_rootfs
 FOR EACH ROW EXECUTE FUNCTION application_standard_artifact_child_guard();
CREATE TRIGGER application_standard_registry_rootfs_current_child_guard BEFORE INSERT OR UPDATE OR DELETE ON deployment_registry_rootfs_current
 FOR EACH ROW EXECUTE FUNCTION application_standard_artifact_child_guard();

-- +goose Down
DROP TABLE deployment_registry_rootfs_current;
DROP TABLE deployment_registry_rootfs;
DROP FUNCTION deployment_registry_rootfs_current_guard();
DROP FUNCTION deployment_registry_rootfs_guard();
DROP FUNCTION lock_deployment_registry_rootfs(uuid);
