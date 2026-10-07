-- filename: 20261005180223326_application_standard_snapshot_restore_authority.sql
-- adr: 593. Raw writers must retain the same fresh catalog authority as Go.
-- Existing catalog/grant bytes and deterministic protobuf hashes are unchanged.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_restore_wire_fields(kind text) RETURNS jsonb
LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE kind
 WHEN 'evidence' THEN '[[1,"version","u32"],[2,"capture_token","string"],[3,"fc_version","string"],[4,"capture","capture"]]'::jsonb
 WHEN 'capture' THEN '[[1,"version","u32"],[2,"parent","receipt"],[3,"memory","artifact"],[4,"vmstate","artifact"],[5,"private_drive","artifact"],[6,"captured_at_unix_nano","i64"]]'::jsonb
 WHEN 'artifact' THEN '[[1,"storage_key","string"],[2,"digest","string"],[3,"bytes","i64"]]'::jsonb
 WHEN 'source' THEN '[[1,"kind","string"],[2,"workload_name","string"],[3,"storage_key","string"],[4,"digest","string"],[5,"bytes","i64"]]'::jsonb
 WHEN 'drive' THEN '[[1,"source","source"],[2,"drive_id","string"],[3,"read_only","bool"],[4,"root_device","bool"],[5,"producer_digest","string"],[6,"producer_bytes","i64"],[7,"injected_digest","string"],[8,"injected_bytes","i64"]]'::jsonb
 WHEN 'consumption' THEN '[[1,"config_hash","string"],[2,"process_pid","u32"],[3,"process_start","string"],[4,"drives","drives"]]'::jsonb
 WHEN 'receipt' THEN '[[1,"binding","binding"],[2,"native_input_hash","string"],[3,"netns","string"],[4,"host_ip","string"],[5,"lease_uid","i32"],[6,"method","i32"],[7,"paused","bool"],[8,"completed_at_unix_nano","i64"],[9,"artifact_consumption","consumption"]]'::jsonb
 WHEN 'binding' THEN '[[1,"protocol_version","u32"],[2,"token","string"],[3,"instance_id","string"],[4,"app_id","string"],[5,"deployment_id","string"],[6,"account_id","string"],[7,"node_id","string"],[8,"incarnation","string"],[9,"desired_revision","i64"],[10,"effective_hash","string"],[11,"captured_input_hash","string"],[12,"payload_hash","string"],[13,"egress_revision","i64"],[14,"issued_at_unix_nano","i64"],[15,"expires_at_unix_nano","i64"],[16,"artifact_sources_hash","string"],[17,"snapshot_capture_token","string"],[18,"snapshot_evidence_hash","string"]]'::jsonb
 END;
$$;

CREATE OR REPLACE FUNCTION application_standard_restore_varint(value bigint) RETURNS bytea
LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE result bytea:=''::bytea; octet integer;
BEGIN
 IF value<0 THEN RAISE EXCEPTION 'restore wire scalar is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale'; END IF;
 LOOP
  octet:=(value%128)::integer; value:=value/128;
  IF value>0 THEN octet:=octet+128; END IF;
  result:=result || decode(lpad(to_hex(octet),2,'0'),'hex');
  EXIT WHEN value=0;
 END LOOP;
 RETURN result;
END;
$$;

-- Only the fixed internal snapshot message tree is supported. Field numbers
-- and proto3 default omission match vmmd.proto; drives retain their wire order.
CREATE OR REPLACE FUNCTION application_standard_restore_wire_field(number integer, kind text, value jsonb) RETURNS bytea
LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE data bytea; n bigint; child jsonb; result bytea:=''::bytea;
BEGIN
 IF kind IN ('u32','i32','i64') THEN
  IF jsonb_typeof(value)<>'number' OR value::text !~ '^(0|[1-9][0-9]{0,18})$' THEN
   RAISE EXCEPTION 'restore wire number is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  n:=value::text::bigint;
  IF kind='u32' AND n>4294967295 OR kind='i32' AND n>2147483647 THEN
   RAISE EXCEPTION 'restore wire number exceeds its type' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  IF n=0 THEN RETURN result; END IF;
  RETURN application_standard_restore_varint(number*8) || application_standard_restore_varint(n);
 ELSIF kind='bool' THEN
  IF jsonb_typeof(value)<>'boolean' THEN RAISE EXCEPTION 'restore wire boolean is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale'; END IF;
  IF value='false'::jsonb THEN RETURN result; END IF;
  RETURN application_standard_restore_varint(number*8) || decode('01','hex');
 ELSIF kind='string' THEN
  IF jsonb_typeof(value)<>'string' THEN RAISE EXCEPTION 'restore wire string is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale'; END IF;
  data:=convert_to(value#>>'{}','UTF8');
  IF octet_length(data)=0 THEN RETURN result; END IF;
 ELSIF kind='drives' THEN
  IF jsonb_typeof(value)<>'array' OR jsonb_array_length(value)>7 THEN RAISE EXCEPTION 'restore wire drives are invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale'; END IF;
  FOR child IN SELECT jsonb_array_elements(value) LOOP
   result:=result || application_standard_restore_wire_field(number,'drive',child);
  END LOOP;
  RETURN result;
 ELSE
  data:=application_standard_restore_wire_message(kind,value);
 END IF;
 RETURN application_standard_restore_varint(number*8+2) || application_standard_restore_varint(octet_length(data)) || data;
EXCEPTION WHEN numeric_value_out_of_range THEN
 RAISE EXCEPTION 'restore wire number exceeds its type' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_restore_wire_message(kind text, value jsonb) RETURNS bytea
LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE fields jsonb:=application_standard_restore_wire_fields(kind); field jsonb; result bytea:=''::bytea; item jsonb;
BEGIN
 IF fields IS NULL OR jsonb_typeof(value)<>'object' THEN
  RAISE EXCEPTION 'restore wire message is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 FOR field IN SELECT jsonb_array_elements(fields) LOOP
  item:=value->(field->>1);
  IF item IS NOT NULL THEN
   result:=result || application_standard_restore_wire_field((field->>0)::integer,field->>2,item);
  END IF;
 END LOOP;
 RETURN result;
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_restore_evidence_hash(token uuid, fc_version text, capture jsonb) RETURNS text
LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT encode(sha256(convert_to('gregale.snapshot-restore-evidence.v1','UTF8') || decode('00','hex') ||
  application_standard_restore_wire_message('evidence',jsonb_build_object('version',1,'capture_token',token::text,'fc_version',fc_version,'capture',capture))),'hex');
$$;

CREATE OR REPLACE FUNCTION application_standard_restore_catalog_matches(b jsonb, input jsonb,
 grant_data jsonb, acknowledgment jsonb, captured_input jsonb, snapshot_row jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE capture jsonb:=acknowledgment->'capture'; parent jsonb:=capture->'parent'->'binding'; ram bigint;
BEGIN
 IF (application_standard_snapshot_positive_integer(input->'instance_ram_mb')
  AND jsonb_typeof(capture)='object' AND capture->>'version'='1'
  AND capture->'parent'=grant_data->'parent' AND capture->'parent'->'paused'='false'::jsonb
  AND application_standard_snapshot_acknowledgment_valid(acknowledgment,grant_data,(acknowledgment->>'completed_at_unix_nano')::bigint)
  AND b->>'snapshot_evidence_hash'=application_standard_restore_evidence_hash((b->>'snapshot_capture_token')::uuid,grant_data->>'fc_version',capture)
  AND b->>'token'<>parent->>'token' AND b->>'instance_id'<>parent->>'instance_id'
  AND b->>'app_id'=parent->>'app_id' AND b->>'account_id'=parent->>'account_id' AND b->>'deployment_id'=parent->>'deployment_id'
  AND b->>'desired_revision'=parent->>'desired_revision' AND b->>'effective_hash'=parent->>'effective_hash'
  AND b->>'egress_revision'=parent->>'egress_revision' AND b->>'artifact_sources_hash'=parent->>'artifact_sources_hash'
  AND application_standard_native_inputs_match(captured_input,input)
  AND snapshot_row->>'storage_key'=capture->'memory'->>'storage_key'
  AND snapshot_row->>'fc_version'=grant_data->>'fc_version'
  AND snapshot_row->>'disk_bytes'=capture->'vmstate'->>'bytes'
  AND (b->>'issued_at_unix_nano')::numeric+5000000000>=(capture->>'captured_at_unix_nano')::numeric) IS NOT TRUE THEN RETURN false; END IF;
 ram:=(input->>'instance_ram_mb')::bigint;
 RETURN ram<=16384 AND ram*1048576=(capture->'memory'->>'bytes')::bigint
  AND ram*1048576=(snapshot_row->>'mem_bytes')::bigint;
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN RETURN false;
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_lock_snapshot_restore(b jsonb, input jsonb) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE token text:=coalesce(b->>'snapshot_capture_token',''); evidence text:=coalesce(b->>'snapshot_evidence_hash','');
 selected record;
BEGIN
 IF token='' AND evidence='' THEN RETURN; END IF;
 IF (b->>'protocol_version'='2' AND jsonb_typeof(b->'snapshot_capture_token')='string'
  AND token ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND token<>'00000000-0000-0000-0000-000000000000' AND jsonb_typeof(b->'snapshot_evidence_hash')='string'
  AND evidence ~ '^[0-9a-f]{64}$') IS NOT TRUE THEN
  RAISE EXCEPTION 'restore binding is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT c.grant_data,c.acknowledgment,c.input_snapshot,to_jsonb(s) AS snapshot_row INTO selected FROM application_standard_snapshot_captures c
 JOIN snapshots s ON s.application_standard_capture_token=c.token
 WHERE c.token=token::uuid AND c.account_id::text=b->>'account_id' AND c.app_id::text=b->>'app_id'
  AND c.deployment_id::text=b->>'deployment_id' AND s.deployment_id=c.deployment_id
  AND c.acknowledgment IS NOT NULL AND c.received_at IS NOT NULL AND NOT s.stale AND NOT s.delete_pending
 ORDER BY s.created_at DESC,s.id DESC LIMIT 1 FOR SHARE OF c,s NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'restore catalog is unavailable' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF application_standard_restore_catalog_matches(b,input,selected.grant_data,selected.acknowledgment,selected.input_snapshot,selected.snapshot_row) IS NOT TRUE
  OR (b->>'expires_at_unix_nano')::bigint<=(extract(epoch FROM clock_timestamp())*1000000000)::bigint THEN
  RAISE EXCEPTION 'restore catalog or current input is stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'restore catalog is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_boot_restore_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE locked jsonb;
BEGIN
 IF TG_OP='UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
 IF coalesce(NEW.binding->>'snapshot_capture_token','')='' AND coalesce(NEW.binding->>'snapshot_evidence_hash','')='' THEN RETURN NEW; END IF;
 locked:=application_standard_lock_native_boot(NEW.instance_id,NEW.expected_state);
 PERFORM application_standard_lock_snapshot_restore(NEW.binding,locked->'input_snapshot');
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS application_standard_boot_restore_guard ON instance_application_standard_boots;
CREATE TRIGGER application_standard_boot_restore_guard BEFORE INSERT OR UPDATE ON instance_application_standard_boots
 FOR EACH ROW EXECUTE FUNCTION application_standard_boot_restore_guard();

CREATE OR REPLACE FUNCTION application_standard_restore_publication_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE b jsonb; locked jsonb;
BEGIN
 IF NEW.application_standard_boot_token IS NULL OR NEW.state NOT IN ('running','warm')
  OR (NEW.application_standard_boot_token IS NOT DISTINCT FROM OLD.application_standard_boot_token AND OLD.state NOT IN ('waking','cold_booting')) THEN RETURN NEW; END IF;
 SELECT binding INTO b FROM instance_application_standard_boots
  WHERE token=NEW.application_standard_boot_token AND instance_id=NEW.id FOR SHARE NOWAIT;
 IF coalesce(b->>'snapshot_capture_token','')='' AND coalesce(b->>'snapshot_evidence_hash','')='' THEN RETURN NEW; END IF;
 locked:=application_standard_lock_native_boot(OLD.id,OLD.state);
 PERFORM application_standard_lock_snapshot_restore(b,locked->'input_snapshot');
 RETURN NEW;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'restore publication is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
DROP TRIGGER IF EXISTS application_standard_restore_publication_guard ON instances;
CREATE TRIGGER application_standard_restore_publication_guard BEFORE UPDATE ON instances
 FOR EACH ROW EXECUTE FUNCTION application_standard_restore_publication_guard();
-- +goose StatementEnd

-- +goose Down
-- Retain fail-closed catalog authority when rolling back the binary. Frozen
-- records stay immutable; older binaries cannot silently certify restored RAM.
