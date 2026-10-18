-- filename: 20261002204249137_application_standard_snapshot_capture_catalog.sql
-- adr: 431. Fresh capture authority and immutable, independently retained lineage.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE application_standard_snapshot_captures (
 token uuid PRIMARY KEY,
 instance_id uuid NOT NULL,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 node_id uuid NOT NULL,
 parent_token uuid NOT NULL,
 memory_key text NOT NULL UNIQUE,
 expected_state text NOT NULL CHECK (expected_state IN ('running','snapshotting','migrating')),
 grant_data jsonb NOT NULL CHECK (jsonb_typeof(grant_data)='object'),
 input_snapshot jsonb NOT NULL CHECK (jsonb_typeof(input_snapshot)='object'),
 acknowledgment jsonb CHECK (jsonb_typeof(acknowledgment)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 received_at timestamptz,
 CHECK ((acknowledgment IS NULL)=(received_at IS NULL))
);
-- Source residency cleanup must not erase lineage: no instance/node/boot FK.
CREATE INDEX application_standard_snapshot_captures_scope
 ON application_standard_snapshot_captures(account_id,app_id,deployment_id);

CREATE FUNCTION application_standard_snapshot_positive_integer(v jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 RETURN coalesce(jsonb_typeof(v)='number' AND v::text ~ '^[1-9][0-9]{0,18}$'
  AND v::text::numeric<=9223372036854775807,false);
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN RETURN false;
END;
$$;

CREATE FUNCTION application_standard_lock_snapshot_capture(instance_id uuid, expected_state text) RETURNS jsonb
LANGUAGE plpgsql AS $$
DECLARE locked jsonb; i instances%ROWTYPE; g instance_application_standard_boots%ROWTYPE;
 r jsonb; b jsonb; deadline timestamptz; now_utc timestamptz;
BEGIN
 IF expected_state NOT IN ('running','snapshotting','migrating') THEN
  RAISE EXCEPTION 'snapshot source state is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_conflict';
 END IF;
 locked:=application_standard_lock_native_boot(instance_id,expected_state);
 SELECT * INTO i FROM instances WHERE id=instance_id;
 SELECT * INTO g FROM instance_application_standard_boots WHERE token=i.application_standard_boot_token FOR SHARE NOWAIT;
 r:=g.receipt; b:=r->'binding';
 IF g.instance_id IS DISTINCT FROM i.id OR r IS NULL OR i.application_standard_promotion_token IS NOT NULL
  OR b->>'protocol_version' IS DISTINCT FROM '2' OR (locked->>'protocol_version')::integer<2
  OR b->>'node_id' IS DISTINCT FROM i.node_id::text OR b->>'incarnation' IS DISTINCT FROM locked->>'incarnation'
  OR b->>'captured_input_hash' IS DISTINCT FROM locked->>'captured_input_hash'
  OR b->>'instance_id' IS DISTINCT FROM i.id::text OR b->>'app_id' IS DISTINCT FROM i.app_id::text
  OR b->>'deployment_id' IS DISTINCT FROM i.deployment_id::text
  OR b->>'account_id' IS DISTINCT FROM locked->'input_snapshot'->>'account_id'
  OR r->>'netns' IS DISTINCT FROM i.netns OR r->>'host_ip' IS DISTINCT FROM host(i.host_ip)
  OR r->>'lease_uid' IS DISTINCT FROM i.guest_uid::text OR r->>'paused' IS DISTINCT FROM 'false'
  OR i.started_at IS NULL THEN
  RAISE EXCEPTION 'snapshot source ownership changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 PERFORM application_standard_native_artifact_protocol(b,r,locked->'input_snapshot',(locked->>'protocol_version')::smallint);
 -- Resident boot history may expire; every NEW capture still needs fresh approval.
 now_utc:=clock_timestamp();
 deadline:=application_standard_native_artifact_deadline(locked->'input_snapshot',now_utc);
 now_utc:=clock_timestamp();
 IF deadline IS NOT NULL AND deadline<=now_utc THEN
  RAISE EXCEPTION 'snapshot approval expired during review' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN locked || jsonb_build_object('parent',r,'source_started_at_unix_nano',(extract(epoch FROM i.started_at)*1000000000)::bigint,
  'clock_unix_nano',(extract(epoch FROM now_utc)*1000000000)::bigint,
  'artifact_expires_at_unix_nano',(extract(epoch FROM deadline)*1000000000)::bigint);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'snapshot source inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;

CREATE FUNCTION application_standard_snapshot_grant_valid(g jsonb, token uuid, parent jsonb, expected_state text,
 source_started bigint, now_nano bigint, deadline bigint) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE prefix text; dep text; mode text;
BEGIN
 IF (jsonb_typeof(g)='object' AND g ?& ARRAY['version','token','parent','memory_key','vmstate_key','private_drive_key',
  'fc_version','mode','before_checkpoint','source_started_at_unix_nano','issued_at_unix_nano','expires_at_unix_nano']
  AND g-ARRAY['version','token','parent','memory_key','vmstate_key','private_drive_key','fc_version','mode',
  'before_checkpoint','source_started_at_unix_nano','issued_at_unix_nano','expires_at_unix_nano']='{}'::jsonb
  AND jsonb_typeof(g->'version')='number' AND g->>'version'='1'
  AND jsonb_typeof(g->'token')='string' AND g->>'token'=token::text
  AND token<>'00000000-0000-0000-0000-000000000000'::uuid AND g->'parent'=parent
  AND jsonb_typeof(g->'memory_key')='string' AND octet_length(g->>'memory_key')<=512
  AND jsonb_typeof(g->'vmstate_key')='string' AND jsonb_typeof(g->'private_drive_key')='string'
  AND jsonb_typeof(g->'fc_version')='string' AND octet_length(g->>'fc_version') BETWEEN 1 AND 128
  AND g->>'fc_version' !~ '[[:space:]/\\]' AND jsonb_typeof(g->'mode')='string'
  AND jsonb_typeof(g->'before_checkpoint')='boolean'
  AND application_standard_snapshot_positive_integer(g->'source_started_at_unix_nano')
  AND application_standard_snapshot_positive_integer(g->'issued_at_unix_nano')
  AND application_standard_snapshot_positive_integer(g->'expires_at_unix_nano')) IS NOT TRUE THEN RETURN false; END IF;
 dep:=parent->'binding'->>'deployment_id'; mode:=g->>'mode';
 IF NOT (mode='warm' AND expected_state='running' AND g->'before_checkpoint'='false'::jsonb
  OR mode='park' AND expected_state='snapshotting'
  OR mode='migration' AND expected_state='migrating' AND g->'before_checkpoint'='false'::jsonb) THEN RETURN false; END IF;
 prefix:='snap/' || dep || CASE WHEN mode='warm' THEN '/warm' ELSE '' END || '/captures/' || token::text || '/v2/';
 IF g->>'memory_key'<>(prefix || 'mem') THEN
  prefix:='snap/' || replace(dep,'-','') || CASE WHEN mode='warm' THEN '/warm' ELSE '' END || '/captures/' || token::text || '/v2/';
 END IF;
 RETURN coalesce(g->>'memory_key'=prefix || 'mem' AND g->>'vmstate_key'=prefix || 'vmstate'
  AND g->>'private_drive_key'=prefix || 'drive'
  AND (g->>'source_started_at_unix_nano')::bigint=source_started
  AND source_started<=(g->>'issued_at_unix_nano')::numeric+5000000000
  AND (g->>'issued_at_unix_nano')::bigint<=now_nano::numeric+5000000000
  AND (g->>'expires_at_unix_nano')::bigint>now_nano
  AND (g->>'expires_at_unix_nano')::numeric>(g->>'issued_at_unix_nano')::numeric
  AND (g->>'expires_at_unix_nano')::numeric-(g->>'issued_at_unix_nano')::numeric<=600000000000
  AND (deadline IS NULL OR (g->>'expires_at_unix_nano')::bigint<=deadline),false);
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN RETURN false;
END;
$$;

CREATE FUNCTION application_standard_snapshot_artifact_valid(a jsonb, key text) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
BEGIN
 RETURN coalesce(jsonb_typeof(a)='object' AND a ?& ARRAY['storage_key','digest','bytes']
  AND a-ARRAY['storage_key','digest','bytes']='{}'::jsonb
  AND jsonb_typeof(a->'storage_key')='string' AND a->>'storage_key'=key
  AND jsonb_typeof(a->'digest')='string' AND a->>'digest' ~ '^sha256:[0-9a-f]{64}$'
  AND application_standard_snapshot_positive_integer(a->'bytes') AND (a->>'bytes')::numeric<=17179869184,false);
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN RETURN false;
END;
$$;

CREATE FUNCTION application_standard_snapshot_acknowledgment_valid(a jsonb, g jsonb, now_nano bigint) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE c jsonb; main_bytes bigint;
BEGIN
 c:=a->'capture';
 IF (jsonb_typeof(a)='object' AND a ?& ARRAY['grant','capture','completed_at_unix_nano']
  AND a-ARRAY['grant','capture','completed_at_unix_nano']='{}'::jsonb AND a->'grant'=g
  AND application_standard_snapshot_positive_integer(a->'completed_at_unix_nano')
  AND jsonb_typeof(c)='object' AND c ?& ARRAY['version','parent','memory','vmstate','private_drive','captured_at_unix_nano']
  AND c-ARRAY['version','parent','memory','vmstate','private_drive','captured_at_unix_nano']='{}'::jsonb
  AND jsonb_typeof(c->'version')='number' AND c->>'version'='1' AND c->'parent'=g->'parent'
  AND application_standard_snapshot_positive_integer(c->'captured_at_unix_nano')
  AND application_standard_snapshot_artifact_valid(c->'memory',g->>'memory_key')
  AND application_standard_snapshot_artifact_valid(c->'vmstate',g->>'vmstate_key')
  AND application_standard_snapshot_artifact_valid(c->'private_drive',g->>'private_drive_key')) IS NOT TRUE THEN RETURN false; END IF;
 SELECT (d->>'injected_bytes')::bigint INTO main_bytes FROM jsonb_array_elements(g->'parent'->'artifact_consumption'->'drives') d
  WHERE application_standard_native_source_role(d->'source')='main';
 RETURN coalesce((c->'private_drive'->>'bytes')::bigint=main_bytes
  AND (c->>'captured_at_unix_nano')::bigint>=(g->'parent'->>'completed_at_unix_nano')::bigint
  AND (c->>'captured_at_unix_nano')::numeric>=(g->>'issued_at_unix_nano')::numeric-5000000000
  AND (a->>'completed_at_unix_nano')::bigint>=(c->>'captured_at_unix_nano')::bigint
  AND (a->>'completed_at_unix_nano')::bigint<(g->>'expires_at_unix_nano')::bigint
  AND (a->>'completed_at_unix_nano')::numeric<=now_nano::numeric+5000000000,false);
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range OR invalid_parameter_value THEN RETURN false;
END;
$$;

CREATE FUNCTION application_standard_snapshot_capture_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE locked jsonb; parent jsonb; b jsonb; now_nano bigint; deadline bigint;
BEGIN
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM apps WHERE id=OLD.app_id) OR NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id)
   OR NOT EXISTS(SELECT 1 FROM accounts WHERE id=OLD.account_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'snapshot lineage is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 IF TG_OP='UPDATE' THEN
  IF NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
  IF (to_jsonb(NEW)-ARRAY['acknowledgment','received_at']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['acknowledgment','received_at'])
   OR OLD.acknowledgment IS NOT NULL OR NEW.acknowledgment IS NULL THEN
   RAISE EXCEPTION 'snapshot lineage is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
  END IF;
 ELSIF NEW.acknowledgment IS NOT NULL OR NEW.received_at IS NOT NULL THEN
  RAISE EXCEPTION 'snapshot acknowledgment requires a saved grant' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 locked:=application_standard_lock_snapshot_capture(NEW.instance_id,NEW.expected_state);
 parent:=locked->'parent'; b:=parent->'binding'; now_nano:=(locked->>'clock_unix_nano')::bigint;
 deadline:=(locked->>'artifact_expires_at_unix_nano')::bigint;
 IF NEW.app_id::text IS DISTINCT FROM b->>'app_id' OR NEW.deployment_id::text IS DISTINCT FROM b->>'deployment_id'
  OR NEW.account_id::text IS DISTINCT FROM b->>'account_id' OR NEW.node_id::text IS DISTINCT FROM b->>'node_id'
  OR NEW.parent_token::text IS DISTINCT FROM b->>'token' OR NEW.memory_key IS DISTINCT FROM NEW.grant_data->>'memory_key'
  OR NOT application_standard_native_inputs_match(NEW.input_snapshot,locked->'input_snapshot')
  OR NOT application_standard_snapshot_grant_valid(NEW.grant_data,NEW.token,parent,NEW.expected_state,
   (locked->>'source_started_at_unix_nano')::bigint,now_nano,deadline) THEN
  RAISE EXCEPTION 'snapshot grant or scope is stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF TG_OP='INSERT' THEN
  IF (NEW.grant_data->>'issued_at_unix_nano')::numeric<now_nano::numeric-5000000000
   OR NEW.input_snapshot IS DISTINCT FROM locked->'input_snapshot' THEN
   RAISE EXCEPTION 'snapshot grant issue inputs changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  NEW.created_at:=clock_timestamp();
 ELSE
  IF NOT application_standard_snapshot_acknowledgment_valid(NEW.acknowledgment,NEW.grant_data,now_nano) THEN
   RAISE EXCEPTION 'snapshot acknowledgment is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  NEW.received_at:=clock_timestamp();
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_snapshot_capture_guard BEFORE INSERT OR UPDATE OR DELETE
 ON application_standard_snapshot_captures FOR EACH ROW EXECUTE FUNCTION application_standard_snapshot_capture_guard();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE application_standard_snapshot_captures;
DROP FUNCTION application_standard_snapshot_capture_guard();
DROP FUNCTION application_standard_snapshot_acknowledgment_valid(jsonb,jsonb,bigint);
DROP FUNCTION application_standard_snapshot_artifact_valid(jsonb,text);
DROP FUNCTION application_standard_snapshot_grant_valid(jsonb,uuid,jsonb,text,bigint,bigint,bigint);
DROP FUNCTION application_standard_lock_snapshot_capture(uuid,text);
DROP FUNCTION application_standard_snapshot_positive_integer(jsonb);
-- +goose StatementEnd
