-- filename: 20261001225440701_application_standard_native_artifact_leases.sql

-- +goose Up
-- +goose StatementBegin
-- adr: 393. Immutable producer captures require independently fresh leases.
-- Go rechecks retained ECDSA evidence under these same input fences. SQL binds
-- private proof provenance, current keys/selections, scan policy and clocks;
-- neither boundary acknowledges the bytes consumed by a native VM.
CREATE OR REPLACE FUNCTION application_standard_native_scan_deadline(input jsonb, scanned_at timestamptz, expires_at timestamptz, now_utc timestamptz, enforce boolean) RETURNS timestamptz
LANGUAGE plpgsql AS $$
DECLARE report jsonb; built timestamptz;
BEGIN
 report:=input->'report'; built:=(report->>'scanner_db_built_at')::timestamptz;
 IF (input->>'status'='complete' AND input->>'scanner_name'='grype'
  AND report->>'scanner_db_status'='valid' AND coalesce(report->>'scanner_version','')<>''
  AND coalesce(report->>'scanner_db_version','')<>'' AND scanned_at<=now_utc AND expires_at>now_utc
  AND built<=now_utc AND built+interval '30 days'>now_utc) IS NOT TRUE THEN
  RAISE EXCEPTION 'native artifact scan is stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF enforce AND ((report->'severity_counts'->>'critical')::integer=0
  AND (report->'severity_counts'->>'high')::integer=0
  AND (report->'severity_counts'->>'unknown')::integer=0) IS NOT TRUE THEN
  RAISE EXCEPTION 'native artifact scan blocks enforcement' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN least(expires_at,built+interval '30 days');
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_native_component_deadline(input jsonb, artifact jsonb, now_utc timestamptz, enforce boolean) RETURNS timestamptz
LANGUAGE plpgsql AS $$
DECLARE f deployment_registry_rootfs%ROWTYPE; s deployment_artifact_scans%ROWTYPE;
        origin deployment_registry_verifications%ROWTYPE; approval deployment_registry_verifications%ROWTYPE;
        owner_inputs jsonb; limit_at timestamptz;
BEGIN
 SELECT p.* INTO f FROM deployment_registry_rootfs_current c JOIN deployment_registry_rootfs p ON p.id=c.artifact_id
 WHERE c.deployment_id=(input->'artifact'->>'id')::uuid AND c.workload_name=artifact->>'workload_name' FOR SHARE OF p NOWAIT;
 SELECT scan.* INTO s FROM deployment_artifact_scan_current c JOIN deployment_artifact_scans scan ON scan.id=c.scan_id
 WHERE c.deployment_id=f.deployment_id AND c.workload_name=f.workload_name FOR SHARE OF scan NOWAIT;
 SELECT * INTO origin FROM deployment_registry_verifications WHERE id=f.registry_verification_id FOR SHARE NOWAIT;
 SELECT * INTO approval FROM deployment_registry_verifications WHERE id=coalesce(s.registry_verification_id,f.registry_verification_id) FOR SHARE NOWAIT;
 IF (f.id::text=artifact->>'producer_id' AND f.input_hash=artifact->>'producer_hash' AND s.rootfs_producer_id=f.id
  AND s.input_snapshot->>'rootfs_input_hash'=f.input_hash AND s.input_snapshot->>'account_id'=input->>'account_id'
  AND s.input_snapshot->>'app_id'=input->>'app_id' AND s.input_snapshot->>'org_id'=input->>'org_id'
  AND s.input_snapshot->>'scope'=input->'artifact'->>'scope' AND s.input_snapshot->>'artifact_digest'=artifact->>'digest'
  AND s.input_snapshot->'artifact_bytes'=artifact->'bytes' AND s.scanned_at>=f.published_at AND f.published_at<=now_utc
  AND f.input_snapshot->>'registry_input_hash'=origin.input_hash AND f.published_at>=origin.verified_at
  AND f.expires_at<=origin.expires_at AND approval.verified_at<=s.scanned_at AND approval.expires_at>now_utc
  AND approval.input_snapshot-'proof'=origin.input_snapshot-'proof'
  AND approval.input_snapshot->'proof'->>'SubjectDigest'=origin.input_snapshot->'proof'->>'SubjectDigest'
  AND (s.registry_verification_id IS NULL AND f.expires_at>now_utc
    OR s.registry_verification_id IS NOT NULL AND s.input_snapshot->>'registry_input_hash'=approval.input_hash)) IS NOT TRUE THEN
  RAISE EXCEPTION 'native artifact approval changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 owner_inputs:=lock_deployment_artifact_scan_with_verification(f.id,s.registry_verification_id);
 IF (approval.app_id::text=input->>'app_id' AND approval.account_id::text=input->>'account_id'
  AND approval.input_snapshot->>'org_id'=input->>'org_id' AND approval.deployment_id=f.deployment_id
  AND approval.workload_name=f.workload_name AND approval.input_snapshot->>'image_reference'=owner_inputs->>'image_reference'
  AND s.input_snapshot->>'image_reference'=owner_inputs->>'image_reference'
  AND encode(sha256(decode(owner_inputs->>'key_der','base64')),'hex')=approval.input_snapshot->'proof'->>'PublisherKeySHA256'
  AND 'sha256:' || encode(sha256(approval.payload),'hex')=approval.input_snapshot->'proof'->>'PayloadDigest'
  AND 'sha256:' || encode(sha256(approval.signature),'hex')=approval.input_snapshot->'proof'->>'SignatureDigest') IS NOT TRUE THEN
  RAISE EXCEPTION 'native publisher approval changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 limit_at:=CASE WHEN s.registry_verification_id IS NULL THEN least(f.expires_at,approval.expires_at) ELSE approval.expires_at END;
 IF s.expires_at>limit_at THEN
  RAISE EXCEPTION 'native scan exceeds publisher approval' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN least(limit_at,application_standard_native_scan_deadline(s.input_snapshot,s.scanned_at,s.expires_at,now_utc,enforce));
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_native_base_deadline(artifact jsonb, now_utc timestamptz, enforce boolean) RETURNS timestamptz
LANGUAGE plpgsql AS $$
DECLARE p base_image_producers%ROWTYPE; s base_image_scans%ROWTYPE;
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.base-producer.' || (artifact->>'storage_key'),0)) THEN
  RAISE EXCEPTION 'native base approval is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 SELECT b.* INTO p FROM base_image_producer_current c JOIN base_image_producers b ON b.id=c.producer_id
 WHERE c.storage_key=artifact->>'storage_key' FOR SHARE OF b NOWAIT;
 SELECT scan.* INTO s FROM base_image_scan_current c JOIN base_image_scans scan ON scan.id=c.scan_id
 WHERE c.storage_key=p.storage_key FOR SHARE OF scan NOWAIT;
 IF (p.id::text=artifact->>'producer_id' AND p.input_hash=artifact->>'producer_hash'
  AND p.input_snapshot->'artifact'=jsonb_build_object('storage_key',artifact->>'storage_key','digest',artifact->>'digest','bytes',artifact->'bytes')
  AND s.base_producer_id=p.id AND s.input_snapshot->>'base_input_hash'=p.input_hash
  AND s.input_snapshot->'artifact'=p.input_snapshot->'artifact'
  AND s.input_snapshot->>'source_reference'=p.input_snapshot->>'source_reference'
  AND s.scanned_at>=p.published_at AND p.published_at<=now_utc) IS NOT TRUE THEN
  RAISE EXCEPTION 'native base scan selection changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN application_standard_native_scan_deadline(s.input_snapshot,s.scanned_at,s.expires_at,now_utc,enforce);
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_native_artifact_deadline(input jsonb, now_utc timestamptz) RETURNS timestamptz
LANGUAGE plpgsql AS $$
DECLARE identity jsonb; artifact jsonb; deadline timestamptz; component_deadline timestamptz; enforce boolean;
BEGIN
 enforce:=input->'settings'->>'security_policy'='enforce'; identity:=input->'runtime_artifacts';
 IF NOT input ? 'runtime_artifacts' THEN
  IF coalesce((input->'settings'->>'require_signed')::boolean,false) OR enforce THEN
   RAISE EXCEPTION 'native signed artifact evidence missing' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  RETURN NULL; -- Compatibility only; this never manufactures producer approval.
 END IF;
 IF (identity->>'format'='gregale.runtime-artifact-input.v1' AND identity->>'account_id'=input->>'account_id'
  AND identity->>'org_id'=input->>'org_id' AND identity->>'app_id'=input->>'app_id'
  AND identity->>'deployment_id'=input->'artifact'->>'id' AND identity->>'scope'=input->'artifact'->>'scope'
  AND jsonb_typeof(identity->'artifacts')='array' AND jsonb_array_length(identity->'artifacts') BETWEEN 1 AND 7) IS NOT TRUE THEN
  RAISE EXCEPTION 'native artifact identity invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 FOR artifact IN SELECT value FROM jsonb_array_elements(identity->'artifacts') LOOP
  IF artifact->>'kind'='base-image' THEN
   component_deadline:=application_standard_native_base_deadline(artifact,now_utc,enforce);
  ELSE
   component_deadline:=application_standard_native_component_deadline(input,artifact,now_utc,enforce);
  END IF;
  deadline:=least(deadline,component_deadline);
 END LOOP;
 RETURN deadline;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'native artifact inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_lock_native_boot(instance_id uuid, expected_state text) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
DECLARE i instances%ROWTYPE; c instance_application_standard_admissions%ROWTYPE;
        input jsonb; incarnation uuid; artifact_deadline timestamptz; now_utc timestamptz;
BEGIN
 SELECT * INTO i FROM instances WHERE id=instance_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR i.state IS DISTINCT FROM expected_state OR i.kind<>'wake' OR i.app_id IS NULL THEN
  RAISE EXCEPTION 'runtime boot state changed' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_conflict';
 END IF;
 SELECT * INTO c FROM instance_application_standard_admissions WHERE instance_application_standard_admissions.instance_id=i.id FOR SHARE NOWAIT;
 input:=application_standard_native_runtime_snapshot(i.app_id,i.deployment_id) || jsonb_build_object('instance_ram_mb',i.ram_mb,'instance_mode',i.mode);
 IF c.instance_id IS NULL OR c.node_id IS DISTINCT FROM i.node_id OR c.input_snapshot IS DISTINCT FROM input
   OR (input->>'desired_revision')::bigint<=0 OR input->>'effective_hash'=''
   OR input->'desired_revision' IS DISTINCT FROM input->'persisted_revision'
   OR (input->'adoptions'='[]'::jsonb AND input->'materialized_fields'='[]'::jsonb) THEN
  RAISE EXCEPTION 'runtime admission inputs changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT vmmd_incarnation INTO incarnation FROM compute_nodes WHERE id=i.node_id FOR SHARE NOWAIT;
 IF incarnation IS NULL THEN
  RAISE EXCEPTION 'native process is not registered' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 now_utc:=clock_timestamp();
 IF expected_state<>'running' THEN
  artifact_deadline:=application_standard_native_artifact_deadline(input,now_utc);
 END IF;
 RETURN jsonb_build_object('artifact_expires_at_unix_nano',(extract(epoch FROM artifact_deadline)*1000000000)::bigint,
  'input_snapshot',input,'captured_input_hash',c.native_input_hash,
  'node_id',i.node_id::text,'incarnation',incarnation::text,'clock_unix_nano',(extract(epoch FROM now_utc)*1000000000)::bigint);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_native_boot_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE locked jsonb; input jsonb; b jsonb; r jsonb; now_nano bigint;
BEGIN
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM instances WHERE id=OLD.instance_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'native boot history is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 IF TG_OP='UPDATE' THEN
  IF NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
  IF NEW.token IS DISTINCT FROM OLD.token OR NEW.instance_id IS DISTINCT FROM OLD.instance_id OR NEW.expected_state IS DISTINCT FROM OLD.expected_state
    OR NEW.binding IS DISTINCT FROM OLD.binding OR NEW.created_at IS DISTINCT FROM OLD.created_at OR OLD.receipt IS NOT NULL OR NEW.receipt IS NULL THEN
   RAISE EXCEPTION 'native boot history is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
  END IF;
 ELSIF NEW.receipt IS NOT NULL THEN
  RAISE EXCEPTION 'native receipt requires a saved grant' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 locked:=application_standard_lock_native_boot(NEW.instance_id,NEW.expected_state);
 input:=locked->'input_snapshot'; b:=NEW.binding;
 now_nano:=(locked->>'clock_unix_nano')::bigint;
 IF (b->>'protocol_version')::integer IS DISTINCT FROM 1 OR b->>'token' IS DISTINCT FROM NEW.token::text
  OR b->>'instance_id' IS DISTINCT FROM NEW.instance_id::text OR b->>'app_id' IS DISTINCT FROM input->>'app_id'
  OR b->>'deployment_id' IS DISTINCT FROM input->'artifact'->>'id' OR b->>'account_id' IS DISTINCT FROM input->>'account_id'
  OR b->>'node_id' IS DISTINCT FROM locked->>'node_id' OR b->>'incarnation' IS DISTINCT FROM locked->>'incarnation'
  OR b->>'captured_input_hash' IS DISTINCT FROM locked->>'captured_input_hash' OR b->>'effective_hash' IS DISTINCT FROM input->>'effective_hash'
  OR (b->>'desired_revision')::bigint IS DISTINCT FROM (input->>'desired_revision')::bigint
  OR (b->>'egress_revision')::bigint IS DISTINCT FROM (input->>'egress_revision')::bigint
  OR coalesce(b->>'payload_hash','') !~ '^[0-9a-f]{64}$'
  OR coalesce((b->>'issued_at_unix_nano')::bigint,0)<=0
  OR coalesce((b->>'expires_at_unix_nano')::bigint,0)<=now_nano
  OR (b->>'expires_at_unix_nano')::bigint <= (b->>'issued_at_unix_nano')::bigint
  OR (locked->>'artifact_expires_at_unix_nano' IS NOT NULL AND (b->>'expires_at_unix_nano')::bigint>(locked->>'artifact_expires_at_unix_nano')::bigint)
  OR (b->>'expires_at_unix_nano')::numeric - (b->>'issued_at_unix_nano')::numeric > 600000000000
  OR (b->>'issued_at_unix_nano')::bigint > now_nano+5000000000 THEN
  RAISE EXCEPTION 'native boot grant is stale or invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF TG_OP='INSERT' AND (b->>'issued_at_unix_nano')::bigint < now_nano-5000000000 THEN
  RAISE EXCEPTION 'native boot issue time is stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF TG_OP='UPDATE' THEN
  r:=NEW.receipt;
  IF r->'binding' IS DISTINCT FROM b OR coalesce(r->>'native_input_hash','') !~ '^[0-9a-f]{64}$'
   OR coalesce(r->>'netns','')='' OR coalesce(r->>'host_ip','')='' OR coalesce((r->>'lease_uid')::integer,0)<=0
   OR coalesce((r->>'method')::integer,-1) NOT IN (0,1) OR (r->>'paused')::boolean IS NULL
   OR coalesce((r->>'completed_at_unix_nano')::bigint,0)<(b->>'issued_at_unix_nano')::bigint-5000000000
   OR (r->>'completed_at_unix_nano')::bigint>= (b->>'expires_at_unix_nano')::bigint
   OR (r->>'completed_at_unix_nano')::bigint>now_nano+5000000000 OR NEW.received_at IS NULL THEN
   RAISE EXCEPTION 'native boot receipt is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
 END IF;
 RETURN NEW;
END;
$_$;

CREATE OR REPLACE FUNCTION application_standard_native_promotion_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $_$
DECLARE locked jsonb; parent jsonb; b jsonb; r jsonb; now_nano bigint;
BEGIN
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM instances WHERE id=OLD.instance_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'native promotion history is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 IF TG_OP='UPDATE' THEN
  IF NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
  IF NEW.token IS DISTINCT FROM OLD.token OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
   OR NEW.parent_token IS DISTINCT FROM OLD.parent_token OR NEW.binding IS DISTINCT FROM OLD.binding
   OR NEW.created_at IS DISTINCT FROM OLD.created_at OR OLD.receipt IS NOT NULL OR NEW.receipt IS NULL THEN
   RAISE EXCEPTION 'native promotion history is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
  END IF;
 ELSIF NEW.receipt IS NOT NULL THEN
  RAISE EXCEPTION 'promotion receipt requires a saved grant' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 locked:=application_standard_lock_native_promotion(NEW.instance_id,false);
 parent:=locked->'parent'; b:=NEW.binding; now_nano:=(locked->>'clock_unix_nano')::bigint;
 IF parent->'binding'->>'token' IS DISTINCT FROM NEW.parent_token::text OR NEW.token=NEW.parent_token
  OR b->>'token' IS DISTINCT FROM NEW.token::text
  OR (b-ARRAY['token','payload_hash','issued_at_unix_nano','expires_at_unix_nano']) IS DISTINCT FROM
     ((parent->'binding')-ARRAY['token','payload_hash','issued_at_unix_nano','expires_at_unix_nano'])
  OR coalesce(b->>'payload_hash','') !~ '^[0-9a-f]{64}$'
  OR coalesce((b->>'issued_at_unix_nano')::bigint,0)<=0
  OR coalesce((b->>'expires_at_unix_nano')::bigint,0)<=now_nano
  OR (b->>'expires_at_unix_nano')::bigint <= (b->>'issued_at_unix_nano')::bigint
  OR (locked->>'artifact_expires_at_unix_nano' IS NOT NULL AND (b->>'expires_at_unix_nano')::bigint>(locked->>'artifact_expires_at_unix_nano')::bigint)
  OR (b->>'expires_at_unix_nano')::numeric-(b->>'issued_at_unix_nano')::numeric >600000000000
  OR (b->>'issued_at_unix_nano')::bigint>now_nano+5000000000
  OR (TG_OP='INSERT' AND (b->>'issued_at_unix_nano')::bigint<now_nano-5000000000) THEN
  RAISE EXCEPTION 'native promotion grant is stale or invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF TG_OP='UPDATE' THEN
  r:=NEW.receipt;
  IF r->'binding' IS DISTINCT FROM b OR (r->>'paused')::boolean IS DISTINCT FROM false
   OR (r-ARRAY['binding','paused','completed_at_unix_nano']) IS DISTINCT FROM (parent-ARRAY['binding','paused','completed_at_unix_nano'])
   OR coalesce((r->>'completed_at_unix_nano')::bigint,0)<(b->>'issued_at_unix_nano')::bigint-5000000000
   OR (r->>'completed_at_unix_nano')::bigint<(parent->>'completed_at_unix_nano')::bigint
   OR (r->>'completed_at_unix_nano')::bigint >= (b->>'expires_at_unix_nano')::bigint
   OR (r->>'completed_at_unix_nano')::bigint>now_nano+5000000000 OR NEW.received_at IS NULL THEN
   RAISE EXCEPTION 'native promotion receipt is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
 END IF;
 RETURN NEW;
END;
$_$;

CREATE OR REPLACE FUNCTION application_standard_native_publication_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE c instance_application_standard_admissions%ROWTYPE; g instance_application_standard_boots%ROWTYPE;
        p instance_application_standard_promotions%ROWTYPE;
        incarnation uuid; b jsonb; r jsonb; managed boolean; publishing boolean; input jsonb; artifact_deadline timestamptz;
BEGIN
 IF NEW.app_id IS NULL OR NEW.kind<>'wake' OR NEW.state NOT IN ('waking','cold_booting','running','warm','migrating') THEN RETURN NEW; END IF;
 SELECT * INTO c FROM instance_application_standard_admissions WHERE instance_id=NEW.id;
 managed:=coalesce(c.input_snapshot->'adoptions'<>'[]'::jsonb OR c.input_snapshot->'materialized_fields'<>'[]'::jsonb,false);
 IF NOT managed THEN RETURN NEW; END IF;
 publishing:=NEW.state IN ('running','warm','migrating') OR coalesce(NEW.netns,'')<>'' OR NEW.host_ip IS NOT NULL OR coalesce(NEW.guest_uid,0)>0;
 IF NOT publishing THEN RETURN NEW; END IF;
 SELECT * INTO g FROM instance_application_standard_boots WHERE token=NEW.application_standard_boot_token FOR SHARE NOWAIT;
 b:=g.binding; r:=g.receipt;
 IF NEW.application_standard_promotion_token IS NOT NULL THEN
  SELECT * INTO p FROM instance_application_standard_promotions WHERE token=NEW.application_standard_promotion_token FOR SHARE NOWAIT;
  IF p.instance_id IS DISTINCT FROM NEW.id OR p.parent_token IS DISTINCT FROM g.token THEN
   RAISE EXCEPTION 'promotion parent changed' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
  b:=p.binding; r:=p.receipt;
 END IF;
 IF TG_OP='UPDATE' AND OLD.application_standard_promotion_token IS NOT NULL AND OLD.application_standard_promotion_token IS DISTINCT FROM NEW.application_standard_promotion_token THEN
  RAISE EXCEPTION 'published promotion identity is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 SELECT vmmd_incarnation INTO incarnation FROM compute_nodes WHERE id=NEW.node_id FOR SHARE NOWAIT;
 IF g.instance_id IS DISTINCT FROM NEW.id OR r IS NULL OR b->>'node_id' IS DISTINCT FROM NEW.node_id::text
  OR b->>'incarnation' IS DISTINCT FROM incarnation::text OR b->>'captured_input_hash' IS DISTINCT FROM c.native_input_hash
  OR r->'binding' IS DISTINCT FROM b OR r->>'netns' IS DISTINCT FROM NEW.netns OR r->>'host_ip' IS DISTINCT FROM host(NEW.host_ip)
  OR (r->>'lease_uid')::integer IS DISTINCT FROM NEW.guest_uid OR (r->>'paused')::boolean IS DISTINCT FROM (NEW.state='warm') THEN
  RAISE EXCEPTION 'managed runtime requires its exact native receipt' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 IF TG_OP='INSERT' OR OLD.state IN ('waking','cold_booting') OR OLD.application_standard_boot_token IS DISTINCT FROM NEW.application_standard_boot_token
  OR OLD.application_standard_promotion_token IS DISTINCT FROM NEW.application_standard_promotion_token THEN
  input:=application_standard_native_runtime_snapshot(NEW.app_id,NEW.deployment_id) || jsonb_build_object('instance_ram_mb',NEW.ram_mb,'instance_mode',NEW.mode);
  IF c.input_snapshot IS DISTINCT FROM input THEN
   RAISE EXCEPTION 'native publication inputs changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  artifact_deadline:=application_standard_native_artifact_deadline(input,clock_timestamp());
  IF (artifact_deadline IS NOT NULL AND (b->>'expires_at_unix_nano')::bigint>(extract(epoch FROM artifact_deadline)*1000000000)::bigint)
   OR (b->>'expires_at_unix_nano')::bigint <= (extract(epoch FROM clock_timestamp())*1000000000)::bigint THEN
   RAISE EXCEPTION 'native runtime authority expired before publication' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 END IF;
 RETURN NEW;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'native publication inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: do not weaken native approval fences or rewrite durable history.
DO $$ BEGIN RAISE WARNING 'application standard native artifact lease fences are retained on Down'; END $$;
-- +goose StatementEnd
