-- filename: 20261111123949621_application_standard_frozen_residency.sql
-- migration-compat: expand
-- ADR-1151: in SQL state lists FROZEN goes wherever WARM goes. Three
-- application-standard functions listed every resident state but frozen:
-- the native residency guard refused FROZEN -> RUNNING, so a frozen instance
-- of a natively managed app could never thaw, and the standard egress target
-- and its guard skipped nodes whose only instance was frozen (implicit state
-- machines audit 2026-11-08, finding 3). The bodies are otherwise unchanged;
-- CREATE OR REPLACE replays safely.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_egress_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE actual jsonb;
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||NEW.app_id::text,0)) THEN
  RAISE EXCEPTION 'standard egress inputs are busy' USING ERRCODE='55P03';
 END IF;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN LATERAL lock_app_resource_account(a.id,false,true) acct ON true
 JOIN app_application_standards e ON e.app_id=a.id WHERE a.id=NEW.app_id FOR SHARE OF a,o,e NOWAIT;
 PERFORM id FROM compute_nodes WHERE id=NEW.node_id FOR SHARE NOWAIT;
 PERFORM id FROM instances WHERE app_id=NEW.app_id AND node_id=NEW.node_id
  AND state IN ('waking','cold_booting','running','snapshotting','migrating','warm','frozen','draining') FOR SHARE NOWAIT;
 actual:=application_standard_egress_target(NEW.app_id,NEW.node_id);
 IF actual IS NULL OR actual<>NEW.target OR actual->>'org_id'<>NEW.org_id::text
  OR actual->'identity'->>'ProtocolVersion'<>'2' OR actual->'identity'->>'Incarnation'=''
  OR NEW.receipt<>jsonb_build_object('identity',actual->'identity','app_id',actual->>'app_id',
   'revision',actual->'policy'->'revision','policy_hash',actual->>'policy_hash') THEN
  RAISE EXCEPTION 'standard egress process or inputs changed' USING ERRCODE='40001',CONSTRAINT='application_standard_egress_current';
 END IF;
 NEW.observed_at:=clock_timestamp();
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_egress_target(app uuid, node uuid) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $_$
 SELECT jsonb_build_object('org_id',a.org_id::text,'app_id',a.id::text,
 'desired_revision',e.desired_revision,'effective_hash',e.effective_hash,
 'identity',jsonb_build_object('ProtocolVersion',coalesce(n.vmmd_admission_protocol,0),'NodeID',n.id::text,'Incarnation',coalesce(n.vmmd_incarnation::text,'')),
 'policy',jsonb_build_object('app_id',a.id::text,'revision',a.egress_allowlist_revision,'allowlist',to_jsonb(a.egress_allowlist::text[]),'ports',to_jsonb(a.egress_ports)),
 'policy_hash',application_standard_egress_policy_hash(a))
 FROM apps a JOIN orgs o ON o.id=a.org_id JOIN app_resource_accounts acct ON acct.app_id=a.id
 JOIN app_application_standards e ON e.app_id=a.id JOIN compute_nodes n ON n.id=node
 WHERE a.id=app AND a.status='active' AND o.status='active' AND NOT o.deleted_pending AND acct.status='active'
 AND e.org_id=a.org_id AND e.project_id IS NOT DISTINCT FROM a.project_id
 AND e.state IN ('persisted','observed') AND e.desired_revision=e.persisted_revision
 AND e.effective_hash~'^[a-f0-9]{64}$' AND (e.exception_expires_at IS NULL OR e.exception_expires_at>clock_timestamp())
 AND n.active AND n.role IS DISTINCT FROM 'control-plane'
 AND EXISTS(SELECT 1 FROM instances i WHERE i.app_id=a.id AND i.node_id=n.id
  AND i.state IN ('waking','cold_booting','running','snapshotting','migrating','warm','frozen','draining'));
$_$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_native_residency_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
 IF NEW.state IN ('running','warm','frozen','migrating') AND OLD.state NOT IN ('waking','cold_booting','running','warm','frozen','migrating') THEN
  RAISE EXCEPTION 'historical native receipt cannot recreate residency' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_egress_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE actual jsonb;
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||NEW.app_id::text,0)) THEN
  RAISE EXCEPTION 'standard egress inputs are busy' USING ERRCODE='55P03';
 END IF;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN LATERAL lock_app_resource_account(a.id,false,true) acct ON true
 JOIN app_application_standards e ON e.app_id=a.id WHERE a.id=NEW.app_id FOR SHARE OF a,o,e NOWAIT;
 PERFORM id FROM compute_nodes WHERE id=NEW.node_id FOR SHARE NOWAIT;
 PERFORM id FROM instances WHERE app_id=NEW.app_id AND node_id=NEW.node_id
  AND state IN ('waking','cold_booting','running','snapshotting','migrating','warm','draining') FOR SHARE NOWAIT;
 actual:=application_standard_egress_target(NEW.app_id,NEW.node_id);
 IF actual IS NULL OR actual<>NEW.target OR actual->>'org_id'<>NEW.org_id::text
  OR actual->'identity'->>'ProtocolVersion'<>'2' OR actual->'identity'->>'Incarnation'=''
  OR NEW.receipt<>jsonb_build_object('identity',actual->'identity','app_id',actual->>'app_id',
   'revision',actual->'policy'->'revision','policy_hash',actual->>'policy_hash') THEN
  RAISE EXCEPTION 'standard egress process or inputs changed' USING ERRCODE='40001',CONSTRAINT='application_standard_egress_current';
 END IF;
 NEW.observed_at:=clock_timestamp();
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_egress_target(app uuid, node uuid) RETURNS jsonb
    LANGUAGE sql STABLE
    AS $_$
 SELECT jsonb_build_object('org_id',a.org_id::text,'app_id',a.id::text,
 'desired_revision',e.desired_revision,'effective_hash',e.effective_hash,
 'identity',jsonb_build_object('ProtocolVersion',coalesce(n.vmmd_admission_protocol,0),'NodeID',n.id::text,'Incarnation',coalesce(n.vmmd_incarnation::text,'')),
 'policy',jsonb_build_object('app_id',a.id::text,'revision',a.egress_allowlist_revision,'allowlist',to_jsonb(a.egress_allowlist::text[]),'ports',to_jsonb(a.egress_ports)),
 'policy_hash',application_standard_egress_policy_hash(a))
 FROM apps a JOIN orgs o ON o.id=a.org_id JOIN app_resource_accounts acct ON acct.app_id=a.id
 JOIN app_application_standards e ON e.app_id=a.id JOIN compute_nodes n ON n.id=node
 WHERE a.id=app AND a.status='active' AND o.status='active' AND NOT o.deleted_pending AND acct.status='active'
 AND e.org_id=a.org_id AND e.project_id IS NOT DISTINCT FROM a.project_id
 AND e.state IN ('persisted','observed') AND e.desired_revision=e.persisted_revision
 AND e.effective_hash~'^[a-f0-9]{64}$' AND (e.exception_expires_at IS NULL OR e.exception_expires_at>clock_timestamp())
 AND n.active AND n.role IS DISTINCT FROM 'control-plane'
 AND EXISTS(SELECT 1 FROM instances i WHERE i.app_id=a.id AND i.node_id=n.id
  AND i.state IN ('waking','cold_booting','running','snapshotting','migrating','warm','draining'));
$_$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.application_standard_native_residency_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
$$;
-- +goose StatementEnd
