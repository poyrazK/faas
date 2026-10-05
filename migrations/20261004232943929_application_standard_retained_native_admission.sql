-- filename: 20261004232943929_application_standard_retained_native_admission.sql
-- adr: 581. An installed standard projection retains native admission after removal.
-- Frozen migrations and captured input hashes remain unchanged.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION application_standard_runtime_requires_native(input jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT coalesce((input->>'persisted_revision')::bigint,0)>0
   OR coalesce(input->'adoptions'<>'[]'::jsonb OR input->'materialized_fields'<>'[]'::jsonb,false);
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_enroll_app()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
DECLARE pins jsonb;
BEGIN
    IF NEW.org_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT coalesce(jsonb_agg(jsonb_build_object('assignment_id', id::text, 'version', admission_version) ORDER BY id), '[]'::jsonb)
    INTO pins FROM application_standard_assignments WHERE active AND org_id = NEW.org_id
      AND ((scope = 'organization' AND scope_id = NEW.org_id)
        OR (scope = 'project' AND scope_id = NEW.project_id) OR (scope = 'application' AND scope_id = NEW.id));
    INSERT INTO app_application_standards (app_id, org_id, project_id, base_settings, adoptions, state)
    VALUES (NEW.id, NEW.org_id, NEW.project_id,
      jsonb_build_object('require_signed', NEW.require_signed, 'security_policy', NEW.security_policy,
        'egress_cidrs', to_jsonb(NEW.egress_allowlist::text[]), 'egress_extra_ports', to_jsonb(NEW.egress_ports)),
      pins, CASE WHEN pins = '[]'::jsonb THEN 'unmanaged' ELSE 'pending' END)
    ON CONFLICT (app_id) DO UPDATE SET org_id = EXCLUDED.org_id, project_id = EXCLUDED.project_id, adoptions = EXCLUDED.adoptions,
      state = CASE WHEN EXCLUDED.adoptions <> '[]'::jsonb OR cardinality(app_application_standards.materialized_fields)>0 OR app_application_standards.persisted_revision>0 THEN 'pending' ELSE 'unmanaged' END,
      desired_revision = app_application_standards.desired_revision + 1, error_code = '', updated_at = now();
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_lock_native_boot(instance_id uuid, expected_state text)
 RETURNS jsonb
 LANGUAGE plpgsql
AS $function$
DECLARE i instances%ROWTYPE; c instance_application_standard_admissions%ROWTYPE;
        input jsonb; incarnation uuid; protocol smallint; artifact_deadline timestamptz; now_utc timestamptz;
BEGIN
 SELECT * INTO i FROM instances WHERE id=instance_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR i.state IS DISTINCT FROM expected_state OR i.kind<>'wake' OR i.app_id IS NULL THEN
  RAISE EXCEPTION 'runtime boot state changed' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_conflict';
 END IF;
 SELECT * INTO c FROM instance_application_standard_admissions WHERE instance_application_standard_admissions.instance_id=i.id FOR SHARE NOWAIT;
 input:=application_standard_native_runtime_snapshot(i.app_id,i.deployment_id) || jsonb_build_object('instance_ram_mb',i.ram_mb,'instance_mode',i.mode);
 IF c.instance_id IS NULL OR c.node_id IS DISTINCT FROM i.node_id OR NOT application_standard_native_inputs_match(c.input_snapshot,input)
   OR (input->>'desired_revision')::bigint<=0 OR input->>'effective_hash'=''
   OR input->'desired_revision' IS DISTINCT FROM input->'persisted_revision'
   OR NOT application_standard_runtime_requires_native(input) THEN
  RAISE EXCEPTION 'runtime admission inputs changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT vmmd_incarnation,vmmd_admission_protocol INTO incarnation,protocol FROM compute_nodes WHERE id=i.node_id FOR SHARE NOWAIT;
 IF incarnation IS NULL THEN
  RAISE EXCEPTION 'native process is not registered' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 now_utc:=clock_timestamp();
 IF expected_state<>'running' THEN
  artifact_deadline:=application_standard_native_artifact_deadline(input,now_utc);
 END IF;
 now_utc:=clock_timestamp();
 IF artifact_deadline IS NOT NULL AND artifact_deadline<=now_utc THEN
  RAISE EXCEPTION 'native artifact lease expired during its locked read' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN jsonb_build_object('artifact_expires_at_unix_nano',(extract(epoch FROM artifact_deadline)*1000000000)::bigint,
  'input_snapshot',input,'captured_input_hash',c.native_input_hash,
  'protocol_version',protocol,'node_id',i.node_id::text,'incarnation',incarnation::text,'clock_unix_nano',(extract(epoch FROM now_utc)*1000000000)::bigint);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_runtime_snapshot(application_id uuid, artifact_id uuid)
 RETURNS jsonb
 LANGUAGE plpgsql
AS $function$
DECLARE a apps%ROWTYPE; e app_application_standards%ROWTYPE;
        acct accounts%ROWTYPE; o orgs%ROWTYPE; d deployments%ROWTYPE; producers jsonb;
BEGIN
 SELECT * INTO a FROM apps WHERE id=application_id FOR SHARE NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'runtime application is missing' USING ERRCODE='23514',CONSTRAINT='application_standards_pending';
 END IF;
 SELECT * INTO o FROM orgs WHERE id=a.org_id FOR SHARE NOWAIT;
 SELECT * INTO acct FROM accounts WHERE id=a.account_id FOR SHARE NOWAIT;
 SELECT * INTO e FROM app_application_standards WHERE app_id=a.id FOR SHARE NOWAIT;
 IF NOT FOUND OR a.status='deleted' OR acct.status NOT IN ('active','past_due') OR acct.abuse_hold_at IS NOT NULL OR o.status NOT IN ('active','past_due') OR o.deleted_pending
   OR e.org_id IS DISTINCT FROM a.org_id OR e.project_id IS DISTINCT FROM a.project_id
   OR (e.exception_expires_at IS NOT NULL AND e.exception_expires_at<=clock_timestamp())
   OR NOT ((e.state='unmanaged' AND e.persisted_revision=0 AND e.adoptions='[]'::jsonb AND cardinality(e.materialized_fields)=0)
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
 IF artifact_id IS NOT NULL THEN producers:=application_standard_runtime_producers(a,d); END IF;
 RETURN application_standard_stable_runtime_input(jsonb_build_object(
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
      FROM deployment_sidecar_layers r WHERE r.deployment_id=d.id),'[]'::jsonb)) END)
  || CASE WHEN e.exception_expires_at IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('exception_expires_at_unix_nano',(extract(epoch FROM e.exception_expires_at)*1000000000)::bigint) END
  || CASE WHEN producers IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('runtime_artifacts',producers) END);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_runtime_inputs_match(captured jsonb, current_input jsonb)
 RETURNS boolean
 LANGUAGE plpgsql
 IMMUTABLE
AS $function$
BEGIN
 IF jsonb_typeof(captured) IS DISTINCT FROM 'object' OR jsonb_typeof(current_input) IS DISTINCT FROM 'object' THEN RETURN false; END IF;
 captured:=application_standard_stable_runtime_input(captured); current_input:=application_standard_stable_runtime_input(current_input);
 IF NOT application_standard_runtime_requires_native(captured)
  AND NOT application_standard_runtime_requires_native(current_input)
  AND captured ? 'account_plan' AND current_input ? 'account_plan' THEN
  captured:=captured-'account_plan'; current_input:=current_input-'account_plan';
 END IF;
 RETURN captured=current_input;
END;
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_instance_runtime_guard()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
DECLARE current_input jsonb; captured_input jsonb; artifact_deadline timestamptz;
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
 IF application_standard_runtime_is_unowned(NEW.app_id) THEN RETURN NEW; END IF;
 current_input := application_standard_native_runtime_snapshot(NEW.app_id,NEW.deployment_id) ||
   jsonb_build_object('instance_ram_mb',NEW.ram_mb,'instance_mode',NEW.mode);
 IF TG_OP='INSERT' THEN
  IF NEW.state IN ('running','warm','migrating') AND
    application_standard_runtime_requires_native(current_input) THEN
   RAISE EXCEPTION 'managed runtime requires a captured boot attempt'
    USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 ELSE
  SELECT input_snapshot INTO captured_input FROM instance_application_standard_admissions WHERE instance_id=NEW.id;
  IF captured_input IS NULL THEN
   -- A legacy instance can continue only while it is still unmanaged. A new
   -- standard requires a fresh instance, never a fabricated historical capture.
   IF application_standard_runtime_requires_native(current_input) THEN
    RAISE EXCEPTION 'managed runtime has no admission capture'
     USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
   END IF;
  ELSE
   -- Legacy unmanaged captures have no native revision; they gain no native
   -- grant authority from this compatibility comparison.
   IF NOT (captured_input ? 'egress_revision') AND NOT application_standard_runtime_requires_native(current_input) THEN
    current_input:=current_input-'egress_revision';
   END IF;
   IF NOT application_standard_runtime_inputs_match(captured_input,current_input) OR
     (captured_input->'account_plan' IS DISTINCT FROM current_input->'account_plan' AND OLD.state IN ('waking','cold_booting')) THEN
   RAISE EXCEPTION 'runtime inputs changed after admission'
    USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
   END IF;
  END IF;
  IF application_standard_runtime_requires_native(current_input) AND current_input ? 'runtime_artifacts' THEN
   artifact_deadline:=application_standard_native_artifact_deadline(current_input,clock_timestamp());
   IF artifact_deadline IS NULL OR artifact_deadline<=clock_timestamp() THEN
    RAISE EXCEPTION 'managed runtime artifact approval expired' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
   END IF;
  END IF;
 END IF;
 RETURN NEW;
END;
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_native_residency_guard()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
DECLARE managed boolean;
BEGIN
 IF OLD.app_id IS NULL OR OLD.kind<>'wake' THEN RETURN NEW; END IF;
 SELECT application_standard_runtime_requires_native(input_snapshot)
  INTO managed FROM instance_application_standard_admissions WHERE instance_id=OLD.id;
 IF NOT coalesce(managed,false) THEN RETURN NEW; END IF;
 IF (OLD.application_standard_boot_token IS NOT NULL AND NEW.application_standard_boot_token IS DISTINCT FROM OLD.application_standard_boot_token)
  OR (OLD.application_standard_promotion_token IS NOT NULL AND NEW.application_standard_promotion_token IS DISTINCT FROM OLD.application_standard_promotion_token) THEN
  RAISE EXCEPTION 'native authority history is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 IF NEW.state IN ('running','warm','migrating') AND OLD.state NOT IN ('waking','cold_booting','running','warm','migrating') THEN
  RAISE EXCEPTION 'historical native receipt cannot recreate residency' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN NEW;
END;
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_native_publication_guard()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
DECLARE c instance_application_standard_admissions%ROWTYPE; g instance_application_standard_boots%ROWTYPE;
        p instance_application_standard_promotions%ROWTYPE;
        incarnation uuid; protocol smallint; b jsonb; r jsonb; managed boolean; publishing boolean; input jsonb; artifact_deadline timestamptz;
BEGIN
 IF NEW.app_id IS NULL OR NEW.kind<>'wake' OR NEW.state NOT IN ('waking','cold_booting','running','warm','migrating') THEN RETURN NEW; END IF;
 SELECT * INTO c FROM instance_application_standard_admissions WHERE instance_id=NEW.id;
 managed:=application_standard_runtime_requires_native(c.input_snapshot);
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
 SELECT vmmd_incarnation,vmmd_admission_protocol INTO incarnation,protocol FROM compute_nodes WHERE id=NEW.node_id FOR SHARE NOWAIT;
 IF g.instance_id IS DISTINCT FROM NEW.id OR r IS NULL OR b->>'node_id' IS DISTINCT FROM NEW.node_id::text
  OR b->>'incarnation' IS DISTINCT FROM incarnation::text OR b->>'captured_input_hash' IS DISTINCT FROM c.native_input_hash
  OR r->'binding' IS DISTINCT FROM b OR r->>'netns' IS DISTINCT FROM NEW.netns OR r->>'host_ip' IS DISTINCT FROM host(NEW.host_ip)
  OR (r->>'lease_uid')::integer IS DISTINCT FROM NEW.guest_uid OR (r->>'paused')::boolean IS DISTINCT FROM (NEW.state='warm') THEN
  RAISE EXCEPTION 'managed runtime requires its exact native receipt' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 PERFORM application_standard_native_artifact_protocol(b,r,c.input_snapshot,protocol);
 IF TG_OP='INSERT' OR OLD.state IN ('waking','cold_booting') OR OLD.application_standard_boot_token IS DISTINCT FROM NEW.application_standard_boot_token
  OR OLD.application_standard_promotion_token IS DISTINCT FROM NEW.application_standard_promotion_token THEN
  input:=application_standard_native_runtime_snapshot(NEW.app_id,NEW.deployment_id) || jsonb_build_object('instance_ram_mb',NEW.ram_mb,'instance_mode',NEW.mode);
  IF NOT application_standard_native_inputs_match(c.input_snapshot,input) THEN
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
$function$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_enrollment_generation_guard()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
BEGIN
    IF NEW.persisted_revision < OLD.persisted_revision THEN
        RAISE EXCEPTION 'application standard installation history regressed'
          USING ERRCODE='23514',CONSTRAINT='application_standard_enrollment_generation';
    END IF;
    IF NEW.lease_generation < OLD.lease_generation THEN
        RAISE EXCEPTION 'application standard enrollment generation regressed'
          USING ERRCODE='23514',CONSTRAINT='application_standard_enrollment_generation';
    END IF;
    IF NEW.app_id IS DISTINCT FROM OLD.app_id THEN
        RAISE EXCEPTION 'application standard enrollment identity changed'
          USING ERRCODE='23514',CONSTRAINT='application_standard_enrollment_identity';
    END IF;
    IF NEW.org_id IS DISTINCT FROM OLD.org_id OR NEW.project_id IS DISTINCT FROM OLD.project_id
      OR NEW.base_settings IS DISTINCT FROM OLD.base_settings OR NEW.local_settings IS DISTINCT FROM OLD.local_settings
      OR NEW.additional_log_destinations IS DISTINCT FROM OLD.additional_log_destinations OR NEW.adoptions IS DISTINCT FROM OLD.adoptions
      OR NEW.desired_revision IS DISTINCT FROM OLD.desired_revision OR NEW.effective IS DISTINCT FROM OLD.effective
      OR NEW.exception_expires_at IS DISTINCT FROM OLD.exception_expires_at
      OR NEW.effective_hash IS DISTINCT FROM OLD.effective_hash OR NEW.materialized_fields IS DISTINCT FROM OLD.materialized_fields THEN
        NEW.lease_owner := ''; NEW.lease_until := NULL;
        NEW.lease_generation := greatest(NEW.lease_generation,OLD.lease_generation+1);
    END IF;
    RETURN NEW;
END;
$function$;
-- +goose StatementEnd

-- +goose Down
-- Retain fail-closed native admission and immutable installation history.
-- A binary rollback cannot restore legacy boot authority for retained projections.
