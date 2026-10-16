-- filename: 20261004043519306_application_standard_exception_authority.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE app_application_standards ADD COLUMN exception_expires_at timestamptz;
CREATE INDEX application_standard_expired_enrollments ON app_application_standards(exception_expires_at,app_id)
 WHERE exception_expires_at IS NOT NULL AND state IN ('persisted','observed');
CREATE TABLE application_standard_exceptions (
 id uuid PRIMARY KEY, org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 standard_id uuid NOT NULL, version bigint NOT NULL CHECK(version>0),
 field text NOT NULL CHECK(field IN ('log_destinations','require_signed','security_policy','trusted_publishers','egress_cidrs','egress_extra_ports')),
 value jsonb NOT NULL CHECK(value<>'null'::jsonb AND octet_length(value::text)<=131072),
 reason text NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 512 AND btrim(reason)<>''),
 approved_by uuid NOT NULL, created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
 revoked_by uuid, revoked_at timestamptz,
 FOREIGN KEY(org_id,standard_id,version) REFERENCES application_standard_versions(org_id,standard_id,version),
 CHECK(expires_at>created_at AND expires_at<=created_at+interval '30 days'),
 CHECK((revoked_by IS NULL)=(revoked_at IS NULL)), CHECK(revoked_at IS NULL OR revoked_at>=created_at)
);
CREATE INDEX application_standard_exception_app_history ON application_standard_exceptions(org_id,app_id,created_at,id);
CREATE FUNCTION application_standard_exception_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actor uuid;
BEGIN
 IF TG_OP='DELETE' THEN
  IF pg_trigger_depth()>1 AND (NOT EXISTS(SELECT 1 FROM orgs WHERE id=OLD.org_id)
    OR NOT EXISTS(SELECT 1 FROM apps WHERE id=OLD.app_id)) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'exception history is retained' USING ERRCODE='23514',CONSTRAINT='application_standard_exception_immutable';
 END IF;
 IF TG_OP='UPDATE' THEN
  IF (to_jsonb(NEW)-ARRAY['revoked_at','revoked_by']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['revoked_at','revoked_by'])
    OR OLD.revoked_at IS NOT NULL OR NEW.revoked_at IS NULL OR NEW.revoked_at>clock_timestamp() THEN
   RAISE EXCEPTION 'exception approval is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_exception_immutable';
  END IF;
  actor:=NEW.revoked_by;
 ELSE
  IF NEW.revoked_at IS NOT NULL OR NEW.expires_at<=clock_timestamp() OR NEW.created_at>clock_timestamp() THEN
   RAISE EXCEPTION 'exception approval time invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_exception_scope';
  END IF;
  actor:=NEW.approved_by;
 END IF;
 PERFORM 1 FROM orgs WHERE id=NEW.org_id AND status='active' AND NOT deleted_pending FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'exception organization unavailable' USING ERRCODE='23514',CONSTRAINT='application_standard_exception_scope'; END IF;
 PERFORM 1 FROM apps WHERE id=NEW.app_id AND org_id=NEW.org_id AND status<>'deleted' FOR UPDATE NOWAIT;
 IF NOT FOUND OR NOT EXISTS(SELECT 1 FROM accounts a JOIN org_memberships m ON m.account_id=a.id
   WHERE a.id=actor AND a.status='active' AND m.org_id=NEW.org_id AND m.removed_at IS NULL AND m.role IN ('owner','admin')) THEN
  RAISE EXCEPTION 'exception authority unavailable' USING ERRCODE='23514',CONSTRAINT='application_standard_exception_scope';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NOT EXISTS(SELECT 1 FROM app_application_standards e CROSS JOIN LATERAL jsonb_array_elements(e.adoptions) pin
    JOIN application_standard_assignments a ON a.id=(pin->>'assignment_id')::uuid
    JOIN application_standard_versions v ON v.org_id=a.org_id AND v.standard_id=a.standard_id AND v.version=(pin->>'version')::bigint
    WHERE e.app_id=NEW.app_id AND a.org_id=NEW.org_id AND v.standard_id=NEW.standard_id AND v.version=NEW.version AND v.definition ? NEW.field)
   OR EXISTS(SELECT 1 FROM application_standard_exceptions x WHERE x.app_id=NEW.app_id AND x.standard_id=NEW.standard_id
    AND x.version=NEW.version AND x.field=NEW.field AND x.revoked_at IS NULL AND x.expires_at>clock_timestamp()) THEN
   RAISE EXCEPTION 'exception adoption or overlap invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_exception_scope';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_exception_guard BEFORE INSERT OR UPDATE OR DELETE ON application_standard_exceptions
 FOR EACH ROW EXECUTE FUNCTION application_standard_exception_guard();

CREATE FUNCTION public.application_standard_exception_deadline(input jsonb, now_utc timestamptz) RETURNS timestamptz
LANGUAGE plpgsql AS $$
DECLARE nano bigint; deadline timestamptz;
BEGIN
 IF NOT input ? 'exception_expires_at_unix_nano' THEN RETURN NULL; END IF;
 IF jsonb_typeof(input->'exception_expires_at_unix_nano') IS DISTINCT FROM 'number'
   OR (input->>'exception_expires_at_unix_nano') !~ '^[1-9][0-9]{0,18}$' THEN
  RAISE EXCEPTION 'exception deadline invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 nano:=(input->>'exception_expires_at_unix_nano')::bigint;
 deadline:=timestamptz 'epoch'+(nano/1000000000)*interval '1 second'+((nano%1000000000)/1000)*interval '1 microsecond';
 IF deadline<=greatest(now_utc,clock_timestamp()) THEN
  RAISE EXCEPTION 'exception authority expired' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN deadline;
EXCEPTION WHEN numeric_value_out_of_range THEN
 RAISE EXCEPTION 'exception deadline invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
END;
$$;

CREATE OR REPLACE FUNCTION public.application_standard_enrollment_generation_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
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
$$;
CREATE OR REPLACE FUNCTION public.application_standard_runtime_snapshot(application_id uuid, artifact_id uuid) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
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
$$;
CREATE OR REPLACE FUNCTION public.application_standard_native_artifact_deadline(input jsonb, now_utc timestamp with time zone) RETURNS timestamp with time zone
    LANGUAGE plpgsql
    AS $$
DECLARE identity jsonb:=input->'runtime_artifacts'; artifact jsonb; s deployment_runtime_scans%ROWTYPE;
 deadline timestamptz; enforce boolean:=input->'settings'->>'security_policy'='enforce'; source_hash text;
BEGIN
 IF NOT input ? 'runtime_artifacts' THEN
  IF coalesce((input->'settings'->>'require_signed')::boolean,false) OR enforce THEN
   RAISE EXCEPTION 'native signed producer evidence missing' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  RETURN application_standard_exception_deadline(input,now_utc);
 END IF;
 IF (identity->>'format'='gregale.runtime-artifact-input.v1' AND identity->>'account_id'=input->>'account_id'
  AND identity->>'org_id'=input->>'org_id' AND identity->>'app_id'=input->>'app_id'
  AND identity->>'deployment_id'=input->'artifact'->>'id' AND identity->>'scope'=input->'artifact'->>'scope') IS NOT TRUE THEN
  RAISE EXCEPTION 'native runtime identity invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 source_hash:=application_standard_native_source_hash(identity->'artifacts');
 IF (SELECT sum((a->>'bytes')::bigint) FROM jsonb_array_elements(identity->'artifacts') a)>34359738368 THEN
  RAISE EXCEPTION 'native runtime sources exceed bounds' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT scan.* INTO s FROM deployment_runtime_scan_current c JOIN deployment_runtime_scans scan ON scan.id=c.scan_id
 WHERE c.deployment_id=(identity->>'deployment_id')::uuid FOR SHARE OF scan NOWAIT;
 IF (s.input_snapshot-ARRAY['facts','status','reports','failure']=identity AND s.input_snapshot->>'status'='complete'
  AND jsonb_typeof(s.input_snapshot->'facts'->'version')='number' AND s.input_snapshot->'facts'->>'version'='1'
  AND (s.input_snapshot->'facts')-ARRAY['version','input_hash','sources_hash','views']='{}'::jsonb
  AND s.input_snapshot->'facts'->>'input_hash'=application_standard_runtime_identity_hash(identity)
  AND s.input_snapshot->'facts'->>'sources_hash'=source_hash AND s.scanned_at<=now_utc AND s.expires_at>now_utc) IS NOT TRUE THEN
  RAISE EXCEPTION 'native composed scan missing or stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 deadline:=least(s.expires_at,application_standard_native_composed_views_deadline(s.input_snapshot,identity,now_utc,enforce));
 FOR artifact IN SELECT value FROM jsonb_array_elements(identity->'artifacts') LOOP
  IF artifact->>'kind'='base-image' THEN PERFORM application_standard_native_base_producer_current(artifact,now_utc);
  ELSE deadline:=least(deadline,application_standard_native_producer_deadline(input,artifact,now_utc)); END IF;
 END LOOP;
 IF deadline<=clock_timestamp() THEN
  RAISE EXCEPTION 'native composed authority expired during read' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN least(deadline,application_standard_exception_deadline(input,now_utc));
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'native composed inputs busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 WHEN invalid_text_representation OR numeric_value_out_of_range OR invalid_parameter_value OR datetime_field_overflow THEN
 RAISE EXCEPTION 'native composed inputs invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_enrollment_generation_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
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
      OR NEW.effective_hash IS DISTINCT FROM OLD.effective_hash OR NEW.materialized_fields IS DISTINCT FROM OLD.materialized_fields THEN
        NEW.lease_owner := ''; NEW.lease_until := NULL;
        NEW.lease_generation := greatest(NEW.lease_generation,OLD.lease_generation+1);
    END IF;
    RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION public.application_standard_runtime_snapshot(application_id uuid, artifact_id uuid) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
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
  || CASE WHEN producers IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('runtime_artifacts',producers) END);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
CREATE OR REPLACE FUNCTION public.application_standard_native_artifact_deadline(input jsonb, now_utc timestamp with time zone) RETURNS timestamp with time zone
    LANGUAGE plpgsql
    AS $$
DECLARE identity jsonb:=input->'runtime_artifacts'; artifact jsonb; s deployment_runtime_scans%ROWTYPE;
 deadline timestamptz; enforce boolean:=input->'settings'->>'security_policy'='enforce'; source_hash text;
BEGIN
 IF NOT input ? 'runtime_artifacts' THEN
  IF coalesce((input->'settings'->>'require_signed')::boolean,false) OR enforce THEN
   RAISE EXCEPTION 'native signed producer evidence missing' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  RETURN NULL;
 END IF;
 IF (identity->>'format'='gregale.runtime-artifact-input.v1' AND identity->>'account_id'=input->>'account_id'
  AND identity->>'org_id'=input->>'org_id' AND identity->>'app_id'=input->>'app_id'
  AND identity->>'deployment_id'=input->'artifact'->>'id' AND identity->>'scope'=input->'artifact'->>'scope') IS NOT TRUE THEN
  RAISE EXCEPTION 'native runtime identity invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 source_hash:=application_standard_native_source_hash(identity->'artifacts');
 IF (SELECT sum((a->>'bytes')::bigint) FROM jsonb_array_elements(identity->'artifacts') a)>34359738368 THEN
  RAISE EXCEPTION 'native runtime sources exceed bounds' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT scan.* INTO s FROM deployment_runtime_scan_current c JOIN deployment_runtime_scans scan ON scan.id=c.scan_id
 WHERE c.deployment_id=(identity->>'deployment_id')::uuid FOR SHARE OF scan NOWAIT;
 IF (s.input_snapshot-ARRAY['facts','status','reports','failure']=identity AND s.input_snapshot->>'status'='complete'
  AND jsonb_typeof(s.input_snapshot->'facts'->'version')='number' AND s.input_snapshot->'facts'->>'version'='1'
  AND (s.input_snapshot->'facts')-ARRAY['version','input_hash','sources_hash','views']='{}'::jsonb
  AND s.input_snapshot->'facts'->>'input_hash'=application_standard_runtime_identity_hash(identity)
  AND s.input_snapshot->'facts'->>'sources_hash'=source_hash AND s.scanned_at<=now_utc AND s.expires_at>now_utc) IS NOT TRUE THEN
  RAISE EXCEPTION 'native composed scan missing or stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 deadline:=least(s.expires_at,application_standard_native_composed_views_deadline(s.input_snapshot,identity,now_utc,enforce));
 FOR artifact IN SELECT value FROM jsonb_array_elements(identity->'artifacts') LOOP
  IF artifact->>'kind'='base-image' THEN PERFORM application_standard_native_base_producer_current(artifact,now_utc);
  ELSE deadline:=least(deadline,application_standard_native_producer_deadline(input,artifact,now_utc)); END IF;
 END LOOP;
 IF deadline<=clock_timestamp() THEN
  RAISE EXCEPTION 'native composed authority expired during read' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN deadline;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'native composed inputs busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
 WHEN invalid_text_representation OR numeric_value_out_of_range OR invalid_parameter_value OR datetime_field_overflow THEN
 RAISE EXCEPTION 'native composed inputs invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
END;
$$;
DROP FUNCTION application_standard_exception_deadline(jsonb,timestamptz);
DROP TABLE application_standard_exceptions;
DROP FUNCTION application_standard_exception_guard();
DROP INDEX application_standard_expired_enrollments;
ALTER TABLE app_application_standards DROP COLUMN exception_expires_at;

-- +goose StatementEnd
