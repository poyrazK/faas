-- filename: 20261002210210135_application_standard_snapshot_capture_scalar_parity.sql
-- adr: 431. JSONB numeric equality must not admit noncanonical nested scalar types.

-- +goose Up
-- +goose StatementBegin
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
  AND token<>'00000000-0000-0000-0000-000000000000'::uuid AND (g->'parent')::text=parent::text
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

CREATE OR REPLACE FUNCTION application_standard_snapshot_acknowledgment_valid(a jsonb, g jsonb, now_nano bigint) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE c jsonb; main_bytes bigint;
BEGIN
 c:=a->'capture';
 IF (jsonb_typeof(a)='object' AND a ?& ARRAY['grant','capture','completed_at_unix_nano']
  AND a-ARRAY['grant','capture','completed_at_unix_nano']='{}'::jsonb AND (a->'grant')::text=g::text
  AND application_standard_snapshot_positive_integer(a->'completed_at_unix_nano')
  AND jsonb_typeof(c)='object' AND c ?& ARRAY['version','parent','memory','vmstate','private_drive','captured_at_unix_nano']
  AND c-ARRAY['version','parent','memory','vmstate','private_drive','captured_at_unix_nano']='{}'::jsonb
  AND jsonb_typeof(c->'version')='number' AND c->>'version'='1' AND (c->'parent')::text=(g->'parent')::text
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

CREATE OR REPLACE FUNCTION application_standard_snapshot_capture_guard() RETURNS trigger
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
   OR NEW.input_snapshot::text IS DISTINCT FROM (locked->'input_snapshot')::text THEN
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
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

CREATE OR REPLACE FUNCTION application_standard_snapshot_acknowledgment_valid(a jsonb, g jsonb, now_nano bigint) RETURNS boolean
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

CREATE OR REPLACE FUNCTION application_standard_snapshot_capture_guard() RETURNS trigger
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
-- +goose StatementEnd
