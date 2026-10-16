-- +goose Up
-- adr: 393. A current signature check can approve the same retained conversion.
-- No producer or prior signature/scan clock is extended or rewritten.
ALTER TABLE deployment_artifact_scans ADD COLUMN registry_verification_id uuid REFERENCES deployment_registry_verifications(id) ON DELETE CASCADE;
ALTER TABLE deployment_artifact_scans ADD CONSTRAINT deployment_artifact_scan_approval_binding CHECK (
 (registry_verification_id IS NULL AND NOT input_snapshot ? 'registry_verification_id' AND NOT input_snapshot ? 'registry_input_hash')
 OR (registry_verification_id IS NOT NULL AND input_snapshot ? 'registry_verification_id' AND input_snapshot ? 'registry_input_hash'
 AND (input_snapshot->>'registry_verification_id'=registry_verification_id::text) IS TRUE AND (input_snapshot->>'registry_input_hash' ~ '^[a-f0-9]{64}$') IS TRUE)
);
-- +goose StatementBegin
CREATE FUNCTION lock_deployment_artifact_scan_with_verification(producer_id uuid, verification_id uuid) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE f deployment_registry_rootfs%ROWTYPE; r deployment_registry_verifications%ROWTYPE; d deployments%ROWTYPE; owner_inputs jsonb;
BEGIN
 SELECT * INTO f FROM deployment_registry_rootfs WHERE id=producer_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'rootfs producer missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 SELECT * INTO r FROM deployment_registry_verifications WHERE id=coalesce(verification_id,f.registry_verification_id);
 IF NOT FOUND THEN RAISE EXCEPTION 'scan verification missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 IF r.deployment_id<>f.deployment_id OR r.workload_name<>f.workload_name THEN
  RAISE EXCEPTION 'scan approval workload changed' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_stale';
 END IF;
 SELECT * INTO d FROM deployments WHERE id=f.deployment_id FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'scan deployment missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing'; END IF;
 owner_inputs:=lock_deployment_registry_verification(r.app_id,r.deployment_id,r.account_id,r.workload_name,r.input_snapshot->'proof'->>'PublisherName');
 RETURN owner_inputs || jsonb_build_object('scope',d.scope,'status',d.status,'storage_now',clock_timestamp());
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'artifact scan renewal inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
END;
$$;
-- +goose StatementEnd
-- +goose Down
DROP FUNCTION lock_deployment_artifact_scan_with_verification(uuid,uuid);
ALTER TABLE deployment_artifact_scans DROP CONSTRAINT deployment_artifact_scan_approval_binding;
ALTER TABLE deployment_artifact_scans DROP COLUMN registry_verification_id;
