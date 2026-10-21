-- +goose Up
-- adr: 393. Private component scan evidence; no native/customer authority.
CREATE TABLE deployment_artifact_scans (
 id uuid PRIMARY KEY,
 rootfs_producer_id uuid NOT NULL,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 workload_name text NOT NULL CHECK (workload_name='' OR (workload_name<>'main' AND workload_name ~ '^[a-z0-9][a-z0-9-]{0,62}$')),
 input_snapshot jsonb NOT NULL CHECK (jsonb_typeof(input_snapshot)='object'),
 input_hash text NOT NULL CHECK (input_hash ~ '^[a-f0-9]{64}$'),
 result_snapshot jsonb NOT NULL CHECK (jsonb_typeof(result_snapshot)='object'),
 scanned_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK (expires_at>scanned_at AND expires_at<=scanned_at+interval '5 minutes'),
 UNIQUE(id,deployment_id,workload_name),
 FOREIGN KEY(rootfs_producer_id,deployment_id,workload_name) REFERENCES deployment_registry_rootfs(id,deployment_id,workload_name) ON DELETE CASCADE,
 CHECK (input_snapshot->>'rootfs_producer_id'=rootfs_producer_id::text AND input_snapshot->>'deployment_id'=deployment_id::text AND input_snapshot->>'workload_name'=workload_name),
 CHECK (input_snapshot->>'status' IN ('complete','failed') AND result_snapshot->>'status'=input_snapshot->>'status')
);
CREATE TABLE deployment_artifact_scan_current (
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 workload_name text NOT NULL CHECK (workload_name='' OR (workload_name<>'main' AND workload_name ~ '^[a-z0-9][a-z0-9-]{0,62}$')),
 scan_id uuid NOT NULL,
 PRIMARY KEY(deployment_id,workload_name),
 FOREIGN KEY(scan_id,deployment_id,workload_name) REFERENCES deployment_artifact_scans(id,deployment_id,workload_name) ON DELETE CASCADE
);
-- +goose StatementBegin
CREATE FUNCTION lock_deployment_artifact_scan(producer_id uuid) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE f deployment_registry_rootfs%ROWTYPE; r deployment_registry_verifications%ROWTYPE; d deployments%ROWTYPE; owner_inputs jsonb;
BEGIN
 SELECT * INTO f FROM deployment_registry_rootfs WHERE id=producer_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'rootfs producer missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 SELECT * INTO r FROM deployment_registry_verifications WHERE id=f.registry_verification_id;
 SELECT * INTO d FROM deployments WHERE id=f.deployment_id FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'scan deployment missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 owner_inputs:=lock_deployment_registry_verification(r.app_id,r.deployment_id,r.account_id,r.workload_name,r.input_snapshot->'proof'->>'PublisherName');
 RETURN owner_inputs || jsonb_build_object('scope',d.scope,'status',d.status,'storage_now',clock_timestamp());
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'artifact scan inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
END;
$$;
CREATE FUNCTION deployment_artifact_scan_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND current_setting('gregale.artifact_scan_insert',true)=NEW.id::text THEN RETURN NEW; END IF;
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'artifact scan evidence is immutable/private' USING ERRCODE='23514',CONSTRAINT='deployment_artifact_scan_immutable';
END;
$$;
CREATE FUNCTION deployment_artifact_scan_current_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN RETURN OLD; END IF;
 IF TG_OP<>'DELETE' AND current_setting('gregale.artifact_scan_insert',true)=NEW.scan_id::text
   AND (TG_OP='INSERT' OR (NEW.deployment_id=OLD.deployment_id AND NEW.workload_name=OLD.workload_name)) THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'artifact scan selection is private' USING ERRCODE='23514',CONSTRAINT='deployment_artifact_scan_immutable';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER artifact_scan_private_guard BEFORE INSERT OR UPDATE OR DELETE ON deployment_artifact_scans
 FOR EACH ROW EXECUTE FUNCTION deployment_artifact_scan_guard();
CREATE TRIGGER artifact_scan_current_private_guard BEFORE INSERT OR UPDATE OR DELETE ON deployment_artifact_scan_current
 FOR EACH ROW EXECUTE FUNCTION deployment_artifact_scan_current_guard();
CREATE TRIGGER application_standard_artifact_scan_child_guard BEFORE INSERT OR UPDATE OR DELETE ON deployment_artifact_scans
 FOR EACH ROW EXECUTE FUNCTION application_standard_artifact_child_guard();
CREATE TRIGGER application_standard_artifact_scan_current_child_guard BEFORE INSERT OR UPDATE OR DELETE ON deployment_artifact_scan_current
 FOR EACH ROW EXECUTE FUNCTION application_standard_artifact_child_guard();

-- +goose Down
DROP TABLE deployment_artifact_scan_current;
DROP TABLE deployment_artifact_scans;
DROP FUNCTION deployment_artifact_scan_current_guard();
DROP FUNCTION deployment_artifact_scan_guard();
DROP FUNCTION lock_deployment_artifact_scan(uuid);
