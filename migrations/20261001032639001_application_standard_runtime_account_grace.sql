-- +goose Up
-- Preserve Account.Active(): past-due workloads serve during the billing grace
-- period; suspended, deleted and abuse-held accounts cannot acquire admission.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_runtime_snapshot(application_id uuid, artifact_id uuid)
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
 IF NOT FOUND OR a.status='deleted' OR acct.status NOT IN ('active','past_due') OR acct.abuse_hold_at IS NOT NULL OR o.status <> 'active' OR o.deleted_pending
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

-- +goose Down
-- Retain the grace-period/abuse-hold correction when downgrading this slice.
