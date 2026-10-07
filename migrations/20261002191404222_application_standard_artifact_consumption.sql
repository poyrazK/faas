-- filename: 20261002191404222_application_standard_artifact_consumption.sql
-- adr: 431. Versioned native consumption, preserving immutable historical grants.

-- +goose Up
-- +goose StatementBegin

ALTER TABLE compute_nodes ADD COLUMN vmmd_admission_protocol smallint NOT NULL DEFAULT 1
 CHECK (vmmd_admission_protocol IN (1,2));

-- Source identities are length-framed UTF-8 bytes, ordered by native role.
-- Producer IDs and renewable approvals remain bound by captured_input_hash.
CREATE FUNCTION application_standard_native_source_role(a jsonb) RETURNS text
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE k text; n text; key text;
BEGIN
 IF jsonb_typeof(a)<>'object' OR jsonb_typeof(a->'kind') IS DISTINCT FROM 'string'
  OR jsonb_typeof(a->'workload_name') IS DISTINCT FROM 'string'
  OR jsonb_typeof(a->'storage_key') IS DISTINCT FROM 'string'
  OR jsonb_typeof(a->'digest') IS DISTINCT FROM 'string'
  OR jsonb_typeof(a->'bytes') IS DISTINCT FROM 'number' THEN RETURN NULL; END IF;
 k:=a->>'kind'; n:=a->>'workload_name'; key:=a->>'storage_key';
 IF key IN ('','.') OR octet_length(key)>512 OR left(key,1)='/' OR right(key,1)='/'
  OR position('..' IN key)>0 OR position('//' IN key)>0
  OR key ~ '(^|/)[.](/|$)' OR position(chr(92) IN key)>0
  OR position(chr(13) IN key)>0 OR position(chr(10) IN key)>0
  OR a->>'digest' !~ '^sha256:[0-9a-f]{64}$'
  OR a->>'bytes' !~ '^[1-9][0-9]{0,10}$'
  OR (a->>'bytes')::bigint>17179869184 THEN RETURN NULL; END IF;
 IF k='base-image' AND n='' THEN RETURN 'base'; END IF;
 IF k IN ('app-layer','full-rootfs') AND n='' THEN RETURN 'main'; END IF;
 IF k='sidecar-layer' AND n<>'main' AND n ~ '^[a-z0-9][a-z0-9-]{0,62}$' THEN RETURN 'sidecar:' || n; END IF;
 RETURN NULL;
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN RETURN NULL;
END;
$$;

CREATE FUNCTION application_standard_native_source_hash(sources jsonb) RETURNS text
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE a jsonb; role text; roles text[]:='{}'; keys text[]:='{}';
 bytes bytea; field text;
BEGIN
 IF jsonb_typeof(sources) IS DISTINCT FROM 'array' THEN
  RAISE EXCEPTION 'native sources are not an array' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF jsonb_array_length(sources) NOT BETWEEN 2 AND 7 THEN
  RAISE EXCEPTION 'native source membership is incomplete' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 bytes:=convert_to('gregale.native-artifact-sources.v2','UTF8') || decode('00','hex')
  || int8send(jsonb_array_length(sources)::bigint);
 FOR a IN SELECT value FROM jsonb_array_elements(sources)
  ORDER BY application_standard_native_source_role(value) COLLATE "C" LOOP
  role:=application_standard_native_source_role(a);
  IF role IS NULL OR role=ANY(roles) OR a->>'storage_key'=ANY(keys) THEN
   RAISE EXCEPTION 'native source membership is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  roles:=array_append(roles,role); keys:=array_append(keys,a->>'storage_key');
  FOREACH field IN ARRAY ARRAY[a->>'kind',a->>'workload_name',a->>'storage_key',a->>'digest'] LOOP
   bytes:=bytes || int8send(octet_length(convert_to(field,'UTF8'))::bigint) || convert_to(field,'UTF8');
  END LOOP;
  bytes:=bytes || int8send((a->>'bytes')::bigint);
 END LOOP;
 IF NOT ('base'=ANY(roles) AND 'main'=ANY(roles)) THEN
  RAISE EXCEPTION 'native base or main is missing' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN encode(sha256(bytes),'hex');
END;
$$;

CREATE FUNCTION application_standard_native_consumed_drive_valid(d jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE a jsonb; role text;
BEGIN
 a:=d->'source'; role:=application_standard_native_source_role(a);
 RETURN coalesce(jsonb_typeof(d)='object' AND role IS NOT NULL
  AND d-ARRAY['source','drive_id','read_only','root_device','producer_digest','producer_bytes','injected_digest','injected_bytes']='{}'::jsonb
  AND a-ARRAY['kind','workload_name','storage_key','digest','bytes']='{}'::jsonb
  AND jsonb_typeof(d->'drive_id')='string' AND octet_length(d->>'drive_id') BETWEEN 1 AND 512
  AND position(chr(13) IN (d->>'drive_id'))=0 AND position(chr(10) IN (d->>'drive_id'))=0
  AND jsonb_typeof(d->'read_only')='boolean' AND d->'read_only'=to_jsonb(role<>'main')
  AND jsonb_typeof(d->'root_device')='boolean' AND d->'root_device'=to_jsonb(role='base')
  AND d->'producer_digest'=a->'digest' AND d->'producer_bytes'=a->'bytes'
  AND d->'injected_bytes'=a->'bytes' AND jsonb_typeof(d->'injected_digest')='string'
  AND d->>'injected_digest' ~ '^sha256:[0-9a-f]{64}$'
  AND (role='main' OR d->'injected_digest'=d->'producer_digest'),false);
END;
$$;

CREATE FUNCTION application_standard_native_consumption_valid(c jsonb, source_hash text) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE d jsonb; sources jsonb:='[]'; ids text[]:='{}';
BEGIN
 IF (jsonb_typeof(c)='object'
  AND c-ARRAY['config_hash','process_pid','process_start','drives']='{}'::jsonb
  AND jsonb_typeof(c->'config_hash')='string' AND c->>'config_hash' ~ '^[0-9a-f]{64}$'
  AND jsonb_typeof(c->'process_pid')='number' AND c->>'process_pid' ~ '^[1-9][0-9]{0,9}$'
  AND (c->>'process_pid')::bigint<=2147483647
  AND jsonb_typeof(c->'process_start')='string' AND c->>'process_start' ~ '^[1-9][0-9]{0,19}$'
  AND (c->>'process_start')::numeric<=18446744073709551615
  AND jsonb_typeof(c->'drives')='array') IS NOT TRUE THEN RETURN false; END IF;
 IF jsonb_array_length(c->'drives') NOT BETWEEN 2 AND 7 THEN RETURN false; END IF;
 FOR d IN SELECT value FROM jsonb_array_elements(c->'drives') LOOP
  IF NOT application_standard_native_consumed_drive_valid(d) OR d->>'drive_id'=ANY(ids) THEN RETURN false; END IF;
  ids:=array_append(ids,d->>'drive_id'); sources:=sources || jsonb_build_array(d->'source');
 END LOOP;
 RETURN application_standard_native_source_hash(sources)=source_hash;
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range OR invalid_parameter_value OR check_violation THEN RETURN false;
END;
$$;

CREATE FUNCTION application_standard_native_artifact_protocol(b jsonb, r jsonb, input jsonb, protocol smallint) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE version integer; actual text;
BEGIN
 IF jsonb_typeof(b->'protocol_version') IS DISTINCT FROM 'number' OR b->>'protocol_version' NOT IN ('1','2') THEN
  RAISE EXCEPTION 'native protocol is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 version:=(b->>'protocol_version')::integer;
 IF version NOT IN (1,2) OR version>protocol THEN
  RAISE EXCEPTION 'native protocol is not registered' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF version=1 THEN
  IF coalesce(b->>'artifact_sources_hash','')<>'' OR (r IS NOT NULL AND r ? 'artifact_consumption') THEN
   RAISE EXCEPTION 'legacy authority cannot acknowledge consumed artifacts' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
  RETURN;
 END IF;
 actual:=application_standard_native_source_hash(input->'runtime_artifacts'->'artifacts');
 IF jsonb_typeof(b->'artifact_sources_hash') IS DISTINCT FROM 'string' OR b->>'artifact_sources_hash' IS DISTINCT FROM actual THEN
  RAISE EXCEPTION 'native source hash differs from captured producers' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF r IS NOT NULL AND (r->'method'='0'::jsonb AND r->'paused'='false'::jsonb
  AND application_standard_native_consumption_valid(r->'artifact_consumption',actual)) IS NOT TRUE THEN
  RAISE EXCEPTION 'native consumed artifact receipt is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_lock_native_boot(instance_id uuid, expected_state text) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
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
   OR (input->'adoptions'='[]'::jsonb AND input->'materialized_fields'='[]'::jsonb) THEN
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
 PERFORM application_standard_native_artifact_protocol(b,NULL,input,(locked->>'protocol_version')::smallint);
 IF (b->>'protocol_version')::integer NOT IN (1,2) OR b->>'protocol_version' IS NULL OR b->>'token' IS DISTINCT FROM NEW.token::text
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
  PERFORM application_standard_native_artifact_protocol(b,r,input,(locked->>'protocol_version')::smallint);
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

CREATE OR REPLACE FUNCTION application_standard_native_publication_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE c instance_application_standard_admissions%ROWTYPE; g instance_application_standard_boots%ROWTYPE;
        p instance_application_standard_promotions%ROWTYPE;
        incarnation uuid; protocol smallint; b jsonb; r jsonb; managed boolean; publishing boolean; input jsonb; artifact_deadline timestamptz;
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
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE WARNING 'native consumed artifact publication fences are retained on Down'; END $$;
-- +goose StatementEnd
