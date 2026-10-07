-- +goose Up
-- This is an input capture, not a native boot receipt or consumer observation.
-- Existing instances are deliberately not backfilled with invented authority.
CREATE TABLE instance_application_standard_admissions (
 instance_id uuid PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
 app_id uuid NOT NULL,
 deployment_id uuid,
 input_snapshot jsonb NOT NULL CHECK (jsonb_typeof(input_snapshot) = 'object'),
 captured_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Capture under nonwaiting parent/control/artifact fences. Instance writers
-- already own a child row; waiting for a parent here would reverse the order
-- used by application deletion, approval and deployment lifecycle writers.
-- +goose StatementBegin
CREATE FUNCTION application_standard_runtime_snapshot(application_id uuid, artifact_id uuid)
RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE a apps%ROWTYPE; e app_application_standards%ROWTYPE;
        acct accounts%ROWTYPE; o orgs%ROWTYPE; d deployments%ROWTYPE;
BEGIN
 SELECT * INTO a FROM apps WHERE id=application_id FOR SHARE NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'runtime application is missing' USING ERRCODE='23514',CONSTRAINT='application_standards_pending';
 END IF;
 SELECT * INTO o FROM orgs WHERE id=a.org_id FOR SHARE NOWAIT;
 SELECT * INTO acct FROM accounts WHERE id=a.account_id FOR SHARE NOWAIT;
 SELECT * INTO e FROM app_application_standards WHERE app_id=a.id FOR SHARE NOWAIT;
 IF NOT FOUND OR a.status='deleted' OR acct.status <> 'active' OR o.status <> 'active' OR o.deleted_pending
   OR e.org_id IS DISTINCT FROM a.org_id OR e.project_id IS DISTINCT FROM a.project_id
   OR NOT ((e.state='unmanaged' AND e.adoptions='[]'::jsonb AND cardinality(e.materialized_fields)=0)
     OR (e.state IN ('persisted','observed') AND e.persisted_revision=e.desired_revision AND e.effective_hash <> '')) THEN
  RAISE EXCEPTION 'runtime application standards are incomplete' USING ERRCODE='23514',CONSTRAINT='application_standards_pending';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.' || a.id::text,0)) THEN
  RAISE EXCEPTION 'runtime controls are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 END IF;
 IF artifact_id IS NOT NULL THEN
  SELECT * INTO d FROM deployments WHERE id=artifact_id FOR SHARE NOWAIT;
  IF NOT FOUND OR d.app_id IS DISTINCT FROM a.id THEN
   RAISE EXCEPTION 'runtime artifact belongs to another application' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_identity';
  END IF;
  IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.artifact-children.' || d.id::text,0)) THEN
   RAISE EXCEPTION 'runtime artifact children are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
  END IF;
 END IF;
 RETURN jsonb_build_object(
  'app_id',a.id::text,'org_id',a.org_id::text,'project_id',coalesce(a.project_id::text,''),'account_id',a.account_id::text,
  'account_plan',acct.plan,'account_egress_allowlist_extra',acct.egress_allowlist_extra,
  'desired_revision',e.desired_revision,'persisted_revision',e.persisted_revision,'effective_hash',e.effective_hash,
  'adoptions',e.adoptions,'materialized_fields',to_jsonb(e.materialized_fields),'effective',e.effective,
  'base_settings',e.base_settings,'local_settings',e.local_settings,'additional_log_destinations',to_jsonb(e.additional_log_destinations::text[]),
  'settings',jsonb_build_object('require_signed',a.require_signed,'security_policy',a.security_policy,
    'egress_cidrs',to_jsonb(a.egress_allowlist::text[]),'egress_extra_ports',to_jsonb(a.egress_ports)),
  'runtime',jsonb_build_object('type',a.type,'runtime',coalesce(a.runtime,''),'ram_mb',a.ram_mb,'app_protocol',a.app_protocol),
  'drains',coalesce((SELECT jsonb_agg(jsonb_build_object('id',r.id::text,'kind',r.kind,'enabled',r.enabled,
    'target_hash',encode(sha256(convert_to(r.target_url,'UTF8')),'hex'),
    'auth_hash',encode(sha256(coalesce(r.auth_header_sealed,''::bytea)),'hex')) ORDER BY r.id)
    FROM app_log_drains r WHERE r.app_id=a.id),'[]'::jsonb),
  'signers',coalesce((SELECT jsonb_agg(jsonb_build_object('name',r.signer_name,
    'fingerprint',encode(sha256(r.cosign_public_key),'hex')) ORDER BY r.signer_name)
    FROM app_trusted_signers r WHERE r.app_id=a.id),'[]'::jsonb),
  'artifact',CASE WHEN artifact_id IS NULL THEN '{}'::jsonb ELSE jsonb_build_object(
    'id',d.id::text,'scope',d.scope,'kind',d.kind,'image_digest',coalesce(d.image_digest,''),
    'rootfs_key',coalesce(d.rootfs_key,''),'rootfs_path',coalesce(d.rootfs_path,''),'rootfs_bytes',coalesce(d.rootfs_bytes,0),
    'source_sha256',coalesce(d.source_sha256,''),'parked_reason',coalesce(d.parked_reason,''),
    'scan_status',d.scan_status,'scan_result_hash',encode(sha256(convert_to(coalesce(d.scan_result::text,''),'UTF8')),'hex'),
    'sidecars',coalesce((SELECT jsonb_agg(jsonb_build_object('sidecar_name',r.sidecar_name,
      'storage_key',r.storage_key,'bytes',r.bytes,'content_digest',r.content_digest) ORDER BY r.sidecar_name)
      FROM deployment_sidecar_layers r WHERE r.deployment_id=d.id),'[]'::jsonb)) END);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
-- +goose StatementEnd

-- Run before the older artifact-child guard takes its shared fence; otherwise
-- two admissions can both take shared locks and fail their exclusive upgrade.
-- +goose StatementBegin
CREATE FUNCTION application_standard_instance_runtime_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_input jsonb; captured_input jsonb;
BEGIN
 IF TG_OP='UPDATE' AND OLD.kind='wake' AND OLD.app_id IS NOT NULL AND
   (NEW.app_id IS DISTINCT FROM OLD.app_id OR NEW.deployment_id IS DISTINCT FROM OLD.deployment_id OR NEW.kind IS DISTINCT FROM OLD.kind) THEN
  RAISE EXCEPTION 'runtime application and artifact identity are immutable'
   USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_identity';
 END IF;
 IF NEW.app_id IS NULL OR NEW.kind <> 'wake' THEN RETURN NEW; END IF;
 -- Cleanup, bookkeeping and nonresident fixtures remain possible while the
 -- standard is pending. Entry into boot, serving, warm and migration is gated.
 IF NEW.state NOT IN ('waking','cold_booting','running','warm','migrating') THEN RETURN NEW; END IF;
 current_input := application_standard_runtime_snapshot(NEW.app_id,NEW.deployment_id) ||
   jsonb_build_object('instance_ram_mb',NEW.ram_mb,'instance_mode',NEW.mode);
 IF TG_OP='INSERT' THEN
  IF NEW.state IN ('running','warm','migrating') AND
    (current_input->'adoptions' <> '[]'::jsonb OR current_input->'materialized_fields' <> '[]'::jsonb) THEN
   RAISE EXCEPTION 'managed runtime requires a captured boot attempt'
    USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 ELSE
  SELECT input_snapshot INTO captured_input FROM instance_application_standard_admissions WHERE instance_id=NEW.id;
  IF captured_input IS NULL THEN
   -- A legacy instance can continue only while it is still unmanaged. A new
   -- standard requires a fresh instance, never a fabricated historical capture.
   IF current_input->'adoptions' <> '[]'::jsonb OR current_input->'materialized_fields' <> '[]'::jsonb THEN
    RAISE EXCEPTION 'managed runtime has no admission capture'
     USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
   END IF;
  ELSIF captured_input IS DISTINCT FROM current_input THEN
   RAISE EXCEPTION 'runtime inputs changed after admission'
    USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_a_runtime_guard BEFORE INSERT OR UPDATE OF app_id,deployment_id,kind,mode,state,netns,host_ip,guest_uid,ram_mb ON instances
 FOR EACH ROW EXECUTE FUNCTION application_standard_instance_runtime_guard();

-- The BEFORE guard retains its locks through this AFTER capture and commit.
-- No other transaction can change a covered input between these two reads.
-- +goose StatementBegin
CREATE FUNCTION application_standard_instance_runtime_capture() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.app_id IS NOT NULL AND NEW.kind='wake' AND NEW.state IN ('waking','cold_booting','running','warm','migrating') THEN
  INSERT INTO instance_application_standard_admissions(instance_id,app_id,deployment_id,input_snapshot)
   VALUES(NEW.id,NEW.app_id,NEW.deployment_id,application_standard_runtime_snapshot(NEW.app_id,NEW.deployment_id) ||
     jsonb_build_object('instance_ram_mb',NEW.ram_mb,'instance_mode',NEW.mode));
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_runtime_capture AFTER INSERT ON instances
 FOR EACH ROW EXECUTE FUNCTION application_standard_instance_runtime_capture();

-- +goose StatementBegin
CREATE FUNCTION application_standard_runtime_capture_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND pg_trigger_depth()>1 THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM instances WHERE id=OLD.instance_id) THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'runtime admission capture is immutable'
  USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_capture_immutable';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_runtime_capture_immutable BEFORE INSERT OR UPDATE OR DELETE ON instance_application_standard_admissions
 FOR EACH ROW EXECUTE FUNCTION application_standard_runtime_capture_immutable();

-- +goose Down
DROP TRIGGER application_standard_runtime_capture_immutable ON instance_application_standard_admissions;
DROP FUNCTION application_standard_runtime_capture_immutable();
DROP TRIGGER application_standard_runtime_capture ON instances;
DROP FUNCTION application_standard_instance_runtime_capture();
DROP TRIGGER application_standard_a_runtime_guard ON instances;
DROP FUNCTION application_standard_instance_runtime_guard();
DROP FUNCTION application_standard_runtime_snapshot(uuid,uuid);
DROP TABLE instance_application_standard_admissions;
