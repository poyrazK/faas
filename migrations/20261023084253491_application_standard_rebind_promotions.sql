-- filename: 20261023084253491_application_standard_rebind_promotions.sql
-- migration-compat: expand
-- ADR-595 extension (docs/adr/20261018-runtime-admission-rebind.md), slice 2:
-- a re-bound admission may be promoted. The fresh promotion grant names the
-- node's current incarnation while its parent names the one that issued it;
-- the promotion lock already accepts that parent only through the
-- instance's re-binds. Nothing else in the binding may differ. Without a
-- re-bind the parent's incarnation is the current one, so the comparison
-- is the one it replaces.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION application_standard_snapshot_resume_valid(b jsonb,r jsonb,parent jsonb,now_nano bigint,held boolean) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE e jsonb:=r->'snapshot_resume_evidence'; old jsonb:=parent->'binding'; command_nano bigint; host_nano bigint;
 hook_nano bigint; completed_nano bigint; parent_nano bigint; payload text;
BEGIN
 IF (jsonb_typeof(e)='object' AND e-ARRAY['version','binding','parent_receipt_hash','resume_command_hash','resume_hook_payload_hash',
    'command_completed_at_unix_nano','host_time_unix_nano','hook_completed_at_unix_nano','completed_at_unix_nano','parent_binding','parent_completed_at_unix_nano']='{}'::jsonb
  AND (SELECT count(*) FROM jsonb_object_keys(e))=11
  AND e->'version'='1'::jsonb AND jsonb_typeof(e->'version')='number' AND e->>'version'='1'
  AND r-ARRAY['binding','native_input_hash','netns','host_ip','lease_uid','method','paused','completed_at_unix_nano','artifact_consumption','snapshot_consumption','snapshot_resume_evidence']='{}'::jsonb
  AND old-ARRAY['protocol_version','token','instance_id','app_id','deployment_id','account_id','node_id','incarnation','desired_revision','effective_hash','captured_input_hash','payload_hash','egress_revision','issued_at_unix_nano','expires_at_unix_nano','artifact_sources_hash','snapshot_capture_token','snapshot_evidence_hash']='{}'::jsonb
  AND old->'protocol_version'='2'::jsonb AND parent->'paused'='true'::jsonb AND parent->'method'='1'::jsonb
  AND NOT(parent ? 'snapshot_resume_evidence') AND r->'paused'='false'::jsonb AND r->'method'='1'::jsonb
  AND r->'binding'=b AND e->'binding'=b AND e->'parent_binding'=old
  AND e->'parent_completed_at_unix_nano'=parent->'completed_at_unix_nano'
  AND (r-ARRAY['binding','paused','completed_at_unix_nano','snapshot_resume_evidence'])=(parent-ARRAY['binding','paused','completed_at_unix_nano'])
  AND (b-ARRAY['token','payload_hash','issued_at_unix_nano','expires_at_unix_nano']-(CASE WHEN held THEN ARRAY['incarnation'] ELSE ARRAY[]::text[] END))
     =(old-ARRAY['token','payload_hash','issued_at_unix_nano','expires_at_unix_nano']-(CASE WHEN held THEN ARRAY['incarnation'] ELSE ARRAY[]::text[] END))
  AND b->>'token'<>old->>'token' AND old->>'token' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND old->>'token'<>'00000000-0000-0000-0000-000000000000' AND old->>'payload_hash' ~ '^[0-9a-f]{64}$'
  AND e->>'parent_receipt_hash'=application_standard_snapshot_resume_parent_hash(parent)
  AND e->>'resume_command_hash'=encode(sha256(convert_to('gregale.runtime-resume.command.v1','UTF8') || decode('00','hex') || convert_to('{"state":"Resumed"}','UTF8')),'hex')
  AND e->>'resume_hook_payload_hash' ~ '^[0-9a-f]{64}$'
  AND e->'completed_at_unix_nano'=r->'completed_at_unix_nano') IS NOT TRUE THEN RETURN false; END IF;
 -- Enforce integer/bool/string wire types, including nested bindings, before casts.
 PERFORM application_standard_restore_wire_message('receipt',r);
 PERFORM application_standard_restore_wire_message('receipt',parent);
 command_nano:=(e->>'command_completed_at_unix_nano')::bigint; host_nano:=(e->>'host_time_unix_nano')::bigint;
 hook_nano:=(e->>'hook_completed_at_unix_nano')::bigint; completed_nano:=(e->>'completed_at_unix_nano')::bigint;
 parent_nano:=(parent->>'completed_at_unix_nano')::bigint;
 payload:=encode(sha256(convert_to('gregale.runtime-promotion.v1','UTF8') || decode('00','hex') ||
  application_standard_restore_wire_message('promotion',jsonb_build_object('parent',parent))),'hex');
 RETURN coalesce(b->>'payload_hash'=payload AND parent_nano>0 AND (old->>'issued_at_unix_nano')::bigint>0
  AND (old->>'expires_at_unix_nano')::numeric>(old->>'issued_at_unix_nano')::numeric
  AND (old->>'expires_at_unix_nano')::numeric-(old->>'issued_at_unix_nano')::numeric<=600000000000
  AND parent_nano::numeric>=(old->>'issued_at_unix_nano')::numeric-5000000000
  AND parent_nano<(old->>'expires_at_unix_nano')::bigint
  AND command_nano>0 AND command_nano>=parent_nano AND command_nano::numeric>=(b->>'issued_at_unix_nano')::numeric-5000000000
  AND host_nano>=command_nano AND hook_nano>=host_nano AND completed_nano>=hook_nano
  AND completed_nano<(b->>'expires_at_unix_nano')::bigint AND completed_nano::numeric<=now_nano::numeric+5000000000,false);
EXCEPTION WHEN data_exception OR check_violation THEN RETURN false;
END;
$$;

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
  OR (b-ARRAY['token','payload_hash','issued_at_unix_nano','expires_at_unix_nano','incarnation']) IS DISTINCT FROM
     ((parent->'binding')-ARRAY['token','payload_hash','issued_at_unix_nano','expires_at_unix_nano','incarnation'])
  OR b->>'incarnation' IS DISTINCT FROM locked->>'incarnation'
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
 IF b->'protocol_version'='2'::jsonb THEN
  PERFORM application_standard_lock_snapshot_restore(b,locked->'input_snapshot');
  IF b->>'payload_hash' IS DISTINCT FROM encode(sha256(convert_to('gregale.runtime-promotion.v1','UTF8') || decode('00','hex') ||
   application_standard_restore_wire_message('promotion',jsonb_build_object('parent',parent))),'hex') THEN
   RAISE EXCEPTION 'native promotion payload differs from parent' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 END IF;
 IF TG_OP='UPDATE' THEN
  r:=NEW.receipt;
  IF r->'binding' IS DISTINCT FROM b OR (r->>'paused')::boolean IS DISTINCT FROM false
   OR (r-ARRAY['binding','paused','completed_at_unix_nano','snapshot_resume_evidence']) IS DISTINCT FROM (parent-ARRAY['binding','paused','completed_at_unix_nano'])
   OR coalesce((r->>'completed_at_unix_nano')::bigint,0)<(b->>'issued_at_unix_nano')::bigint-5000000000
   OR (r->>'completed_at_unix_nano')::bigint<(parent->>'completed_at_unix_nano')::bigint
   OR (r->>'completed_at_unix_nano')::bigint >= (b->>'expires_at_unix_nano')::bigint
   OR (r->>'completed_at_unix_nano')::bigint>now_nano+5000000000 OR NEW.received_at IS NULL THEN
   RAISE EXCEPTION 'native promotion receipt is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
  IF b->'protocol_version'='2'::jsonb THEN
   IF NOT application_standard_snapshot_resume_valid(b,r,parent,now_nano,b->>'incarnation' IS DISTINCT FROM parent->'binding'->>'incarnation') THEN
    RAISE EXCEPTION 'native measured resume proof is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
   END IF;
   PERFORM application_standard_native_artifact_protocol(b,r,locked->'input_snapshot',(locked->>'protocol_version')::smallint);
  ELSIF r ? 'snapshot_resume_evidence' THEN
   RAISE EXCEPTION 'legacy promotion cannot acknowledge measured resume' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
 END IF;
 RETURN NEW;
END;
$_$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
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
 IF b->'protocol_version'='2'::jsonb THEN
  PERFORM application_standard_lock_snapshot_restore(b,locked->'input_snapshot');
  IF b->>'payload_hash' IS DISTINCT FROM encode(sha256(convert_to('gregale.runtime-promotion.v1','UTF8') || decode('00','hex') ||
   application_standard_restore_wire_message('promotion',jsonb_build_object('parent',parent))),'hex') THEN
   RAISE EXCEPTION 'native promotion payload differs from parent' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 END IF;
 IF TG_OP='UPDATE' THEN
  r:=NEW.receipt;
  IF r->'binding' IS DISTINCT FROM b OR (r->>'paused')::boolean IS DISTINCT FROM false
   OR (r-ARRAY['binding','paused','completed_at_unix_nano','snapshot_resume_evidence']) IS DISTINCT FROM (parent-ARRAY['binding','paused','completed_at_unix_nano'])
   OR coalesce((r->>'completed_at_unix_nano')::bigint,0)<(b->>'issued_at_unix_nano')::bigint-5000000000
   OR (r->>'completed_at_unix_nano')::bigint<(parent->>'completed_at_unix_nano')::bigint
   OR (r->>'completed_at_unix_nano')::bigint >= (b->>'expires_at_unix_nano')::bigint
   OR (r->>'completed_at_unix_nano')::bigint>now_nano+5000000000 OR NEW.received_at IS NULL THEN
   RAISE EXCEPTION 'native promotion receipt is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
  IF b->'protocol_version'='2'::jsonb THEN
   IF NOT application_standard_snapshot_resume_valid(b,r,parent,now_nano) THEN
    RAISE EXCEPTION 'native measured resume proof is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
   END IF;
   PERFORM application_standard_native_artifact_protocol(b,r,locked->'input_snapshot',(locked->>'protocol_version')::smallint);
  ELSIF r ? 'snapshot_resume_evidence' THEN
   RAISE EXCEPTION 'legacy promotion cannot acknowledge measured resume' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
 END IF;
 RETURN NEW;
END;
$_$;
DROP FUNCTION application_standard_snapshot_resume_valid(jsonb, jsonb, jsonb, bigint, boolean);
-- +goose StatementEnd
