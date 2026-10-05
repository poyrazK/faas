-- filename: 20261005221849892_application_standard_snapshot_serving_parent.sql
-- adr: 595. Fresh capture grants use the actual measured serving receipt.
-- Previously issued migration SQL and historical evidence remain immutable.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_lock_snapshot_capture(instance_id uuid, expected_state text) RETURNS jsonb
LANGUAGE plpgsql AS $$
DECLARE locked jsonb; i instances%ROWTYPE; g instance_application_standard_boots%ROWTYPE;
 p instance_application_standard_promotions%ROWTYPE; r jsonb; b jsonb; deadline timestamptz; now_utc timestamptz;
BEGIN
 IF expected_state NOT IN ('running','snapshotting','migrating') THEN
  RAISE EXCEPTION 'snapshot source state is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_conflict';
 END IF;
 locked:=application_standard_lock_native_boot(instance_id,expected_state);
 SELECT * INTO i FROM instances WHERE id=instance_id;
 SELECT * INTO g FROM instance_application_standard_boots WHERE token=i.application_standard_boot_token FOR SHARE NOWAIT;
 r:=g.receipt;
 IF i.application_standard_promotion_token IS NOT NULL THEN
  SELECT * INTO p FROM instance_application_standard_promotions WHERE token=i.application_standard_promotion_token FOR SHARE NOWAIT;
  IF NOT FOUND OR p.instance_id IS DISTINCT FROM i.id OR p.parent_token IS DISTINCT FROM g.token
   OR p.receipt IS NULL OR p.received_at IS NULL OR p.receipt->'binding' IS DISTINCT FROM p.binding THEN
   RAISE EXCEPTION 'serving promotion ownership changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  r:=p.receipt;
 ELSIF r->'binding' IS DISTINCT FROM g.binding THEN
  RAISE EXCEPTION 'serving boot ownership changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 b:=r->'binding';
 IF g.instance_id IS DISTINCT FROM i.id OR r IS NULL
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

CREATE OR REPLACE FUNCTION application_standard_snapshot_grant_valid(g jsonb, token uuid, parent jsonb, expected_state text,
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
  AND token<>'00000000-0000-0000-0000-000000000000'::uuid
  AND g->>'token' IS DISTINCT FROM parent->'snapshot_consumption'->>'capture_token' AND (g->'parent')::text=parent::text
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
-- +goose StatementEnd

-- +goose Down
-- Retain actual serving-parent ownership and fresh namespace checks on binary rollback.
