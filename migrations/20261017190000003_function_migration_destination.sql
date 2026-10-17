-- filename: 20261017190000003_function_migration_destination.sql
-- adr: 1577. Immutable migration epochs retain the original admission capture.
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE function_migration_sources ADD COLUMN IF NOT EXISTS capture_result jsonb
 CHECK (capture_result IS NULL OR (capture_token IS NOT NULL AND jsonb_typeof(capture_result)='object'));
CREATE TABLE IF NOT EXISTS function_migration_destinations (
 token uuid PRIMARY KEY CHECK (token<>'00000000-0000-0000-0000-000000000000'),
 instance_id uuid NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
 lease_token text NOT NULL CHECK (lease_token LIKE 'function-v1:%'),
 node_id uuid NOT NULL REFERENCES compute_nodes(id),
 wake_id uuid NOT NULL CHECK (wake_id<>'00000000-0000-0000-0000-000000000000'),
 phase text NOT NULL CHECK (phase IN ('issued','ready','committed','retired')),
 grant_data jsonb NOT NULL CHECK (jsonb_typeof(grant_data)='object'),
 receipt jsonb CHECK (receipt IS NULL OR jsonb_typeof(receipt)='object'),
 UNIQUE(instance_id,lease_token),
 UNIQUE(instance_id,wake_id),
 CHECK ((grant_data->>'version'='1' AND grant_data->'binding'->>'token'=token::text
  AND grant_data->'binding'->>'instance_id'=instance_id::text AND grant_data->'binding'->>'node_id'=node_id::text
  AND grant_data->>'wake_id'=wake_id::text AND grant_data->'source'->>'lease_token'=lease_token
  AND grant_data->'binding'->>'protocol_version'='2'
  AND grant_data->'binding'->>'node_id'<>grant_data->'source'->'parent'->'binding'->>'node_id') IS TRUE),
 CHECK ((phase IN ('issued','retired') OR receipt IS NOT NULL)
  AND (receipt IS NULL OR (receipt->'binding'=grant_data->'binding' AND receipt->>'paused'='false' AND receipt->>'method'='1') IS TRUE))
);
-- A pending destination is physical residency even while its instance row is
-- still owned by the source. These fences survive either scheduler restarting.
CREATE OR REPLACE FUNCTION function_migration_check_capacity(node uuid, extra_ram bigint, extra_cpu bigint, extra_vcpu bigint, exclude_id uuid) RETURNS void
 LANGUAGE plpgsql AS $$
DECLARE n compute_nodes%ROWTYPE; used_ram bigint; used_cpu bigint; used_vcpu bigint; p service_capacity_policy%ROWTYPE;
BEGIN
 PERFORM pg_advisory_xact_lock(1852793957,hashtext(node::text));
 SELECT * INTO n FROM compute_nodes WHERE id=node FOR SHARE NOWAIT;
 SELECT * INTO p FROM service_capacity_policy WHERE singleton;
 IF n.id IS NULL OR n.lifecycle<>'active' OR n.admission_ceiling_mb<=0 OR n.vpcpus<=0 OR n.vcpu_budget<=0 THEN
  RAISE EXCEPTION 'migration target is not sized and active' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_capacity';
 END IF;
 SELECT coalesce(sum(ram),0),coalesce(sum(cpu),0),coalesce(sum(vcpu),0) INTO used_ram,used_cpu,used_vcpu FROM (
  SELECT i.capacity_ram_mb ram,i.capacity_cpu_millicores cpu,i.capacity_vcpu vcpu FROM instances i
   WHERE i.node_id=node AND i.id IS DISTINCT FROM exclude_id AND i.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating','frozen')
  UNION ALL SELECT i.capacity_ram_mb,i.capacity_cpu_millicores,i.capacity_vcpu FROM function_migration_destinations d JOIN instances i ON i.id=d.instance_id
   WHERE d.node_id=node AND d.phase IN ('issued','ready')
 ) resident;
 IF used_ram+extra_ram>n.admission_ceiling_mb OR used_cpu+extra_cpu>n.vpcpus::bigint*1000*p.cpu_overcommit OR used_vcpu+extra_vcpu>n.vcpu_budget THEN
  RAISE EXCEPTION 'migration destination capacity is reserved' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_capacity';
 END IF;
END;
$$;
CREATE OR REPLACE FUNCTION function_migration_destination_capacity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE i instances%ROWTYPE;
BEGIN
 SELECT * INTO i FROM instances WHERE id=NEW.instance_id;
 PERFORM function_migration_check_capacity(NEW.node_id,i.capacity_ram_mb,i.capacity_cpu_millicores,i.capacity_vcpu,NULL);
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS function_migration_destination_capacity_guard ON function_migration_destinations;
CREATE TRIGGER function_migration_destination_capacity_guard BEFORE INSERT ON function_migration_destinations
 FOR EACH ROW EXECUTE FUNCTION function_migration_destination_capacity_guard();
CREATE OR REPLACE FUNCTION function_migration_instance_capacity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state NOT IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating','frozen') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND OLD.node_id IS NOT DISTINCT FROM NEW.node_id AND OLD.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating','frozen')
  AND NEW.capacity_ram_mb<=OLD.capacity_ram_mb AND NEW.capacity_cpu_millicores<=OLD.capacity_cpu_millicores AND NEW.capacity_vcpu<=OLD.capacity_vcpu THEN RETURN NEW; END IF;
 -- Serialize before inspecting pending claims: the first destination may still
 -- be uncommitted, so testing EXISTS before taking this lock would miss it.
 PERFORM pg_advisory_xact_lock(1852793957,hashtext(NEW.node_id::text));
 IF EXISTS (SELECT 1 FROM function_migration_destinations WHERE node_id=NEW.node_id AND phase IN ('issued','ready')) THEN
  PERFORM function_migration_check_capacity(NEW.node_id,NEW.capacity_ram_mb,NEW.capacity_cpu_millicores,NEW.capacity_vcpu,NEW.id);
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS zz_function_migration_instance_capacity_guard ON instances;
CREATE TRIGGER zz_function_migration_instance_capacity_guard BEFORE INSERT OR UPDATE OF node_id,state,capacity_ram_mb,capacity_cpu_millicores,capacity_vcpu ON instances
 FOR EACH ROW EXECUTE FUNCTION function_migration_instance_capacity_guard();
CREATE OR REPLACE FUNCTION function_migration_runtime_receipt(id uuid, node uuid, wake uuid) RETURNS jsonb
 LANGUAGE sql STABLE AS $$
 SELECT receipt FROM function_migration_destinations
 WHERE instance_id=id AND node_id=node AND wake_id=wake AND phase='committed'
$$;
CREATE OR REPLACE FUNCTION function_migration_destination_history_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS (SELECT 1 FROM instances WHERE id=OLD.instance_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'migration destination history is immutable' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_immutable';
 END IF;
 IF NEW.token IS DISTINCT FROM OLD.token OR NEW.instance_id IS DISTINCT FROM OLD.instance_id OR NEW.node_id IS DISTINCT FROM OLD.node_id
  OR NEW.wake_id IS DISTINCT FROM OLD.wake_id OR NEW.lease_token IS DISTINCT FROM OLD.lease_token OR NEW.grant_data IS DISTINCT FROM OLD.grant_data
  OR (OLD.receipt IS NOT NULL AND NEW.receipt IS DISTINCT FROM OLD.receipt)
  OR NOT (NEW.phase=OLD.phase OR OLD.phase='issued' AND NEW.phase IN ('ready','retired') OR OLD.phase='ready' AND NEW.phase IN ('committed','retired')) THEN
  RAISE EXCEPTION 'migration destination history is immutable' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_immutable';
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS function_migration_destination_history_guard ON function_migration_destinations;
CREATE TRIGGER function_migration_destination_history_guard BEFORE UPDATE OR DELETE ON function_migration_destinations
 FOR EACH ROW EXECUTE FUNCTION function_migration_destination_history_guard();
CREATE OR REPLACE FUNCTION function_migration_destination_lease_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS (SELECT 1 FROM function_migration_destinations WHERE instance_id=OLD.instance_id AND lease_token=OLD.lease_token AND phase IN ('issued','ready')) THEN
  RAISE EXCEPTION 'destination cleanup is unconfirmed' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_pending';
 END IF;
 RETURN OLD;
END;
$$;
DROP TRIGGER IF EXISTS function_migration_destination_lease_guard ON migration_leases;
CREATE TRIGGER function_migration_destination_lease_guard BEFORE DELETE ON migration_leases
 FOR EACH ROW EXECUTE FUNCTION function_migration_destination_lease_guard();
CREATE OR REPLACE FUNCTION application_standard_native_artifact_protocol(b jsonb,r jsonb,input jsonb,protocol smallint) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE version integer; actual text; captured jsonb; paused_load boolean; parent jsonb;
BEGIN
 IF jsonb_typeof(b->'protocol_version') IS DISTINCT FROM 'number' OR b->>'protocol_version' NOT IN ('1','2') THEN
  RAISE EXCEPTION 'native protocol is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 version:=(b->>'protocol_version')::integer;
 IF version>protocol THEN
  RAISE EXCEPTION 'native protocol is not registered' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF version<>2 AND EXISTS(SELECT 1 FROM jsonb_array_elements(input->'runtime_artifacts'->'artifacts') a
  WHERE a->>'kind' IN ('source-app-layer','function-layer')) THEN
  RAISE EXCEPTION 'source native byte capability required' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF coalesce((input->>'persisted_revision')::bigint,0)>0 AND input->'adoptions'='[]'::jsonb
  AND input->'materialized_fields'='[]'::jsonb AND version<>2 THEN
  RAISE EXCEPTION 'retained native byte capability required' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF version=1 THEN
  IF coalesce(b->>'artifact_sources_hash','')<>'' OR (r IS NOT NULL AND (r ? 'artifact_consumption' OR r ? 'snapshot_consumption' OR r ? 'snapshot_resume_evidence')) THEN
   RAISE EXCEPTION 'legacy authority cannot acknowledge consumed artifacts' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
  RETURN;
 END IF;
 actual:=application_standard_native_source_hash(input->'runtime_artifacts'->'artifacts');
 IF jsonb_typeof(b->'artifact_sources_hash') IS DISTINCT FROM 'string' OR b->>'artifact_sources_hash' IS DISTINCT FROM actual THEN
  RAISE EXCEPTION 'native source hash differs from captured producers' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF r IS NULL THEN RETURN; END IF;
 IF (jsonb_typeof(r->'method')='number' AND r->>'method' IN ('0','1')) IS NOT TRUE THEN
  RAISE EXCEPTION 'native method is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 IF (jsonb_typeof(r->'paused')='boolean' AND application_standard_native_consumption_valid(r->'artifact_consumption',actual)) IS NOT TRUE THEN
  RAISE EXCEPTION 'native consumed artifact receipt is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 IF r->'method'='0'::jsonb AND r->'paused'='false'::jsonb AND NOT (r ? 'snapshot_consumption' OR r ? 'snapshot_resume_evidence') THEN RETURN; END IF;
 paused_load:=r->'paused'='true'::jsonb OR r ? 'snapshot_resume_evidence';
 IF (r->'method'='1'::jsonb AND r ? 'snapshot_consumption'
  AND r-ARRAY['binding','native_input_hash','netns','host_ip','lease_uid','method','paused','completed_at_unix_nano','artifact_consumption','snapshot_consumption','snapshot_resume_evidence']='{}'::jsonb
  AND r->'artifact_consumption'->>'config_hash'=encode(sha256(convert_to('{"mem_backend":{"backend_path":"snap-in-mem","backend_type":"File"},"resume_vm":' || CASE WHEN paused_load THEN 'false' ELSE 'true' END || ',"snapshot_path":"snap-in-vmstate"}','UTF8')),'hex')) IS NOT TRUE THEN
  RAISE EXCEPTION 'native restore receipt is incomplete' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 IF r ? 'snapshot_resume_evidence' THEN
  parent:=application_standard_snapshot_resume_parent(r);
  IF NOT application_standard_snapshot_resume_valid(b,r,parent,(r->>'completed_at_unix_nano')::bigint) THEN
   RAISE EXCEPTION 'native resume lineage is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
 END IF;
 SELECT c.acknowledgment->'capture' INTO captured FROM application_standard_snapshot_captures c
 WHERE c.token::text=b->>'snapshot_capture_token' AND c.account_id::text=b->>'account_id' AND c.app_id::text=b->>'app_id'
  AND c.deployment_id::text=b->>'deployment_id' AND c.acknowledgment IS NOT NULL AND c.received_at IS NOT NULL FOR SHARE NOWAIT;
 IF NOT FOUND THEN
  -- This exact destination epoch replaces catalog authority for its logical
  -- migration ID. It cannot authorize an ordinary clone or another payload.
  SELECT d.grant_data->'restore'->'capture' INTO captured FROM function_migration_destinations d
  WHERE d.token::text=b->>'token' AND d.instance_id::text=b->>'instance_id' AND d.node_id::text=b->>'node_id'
   AND d.phase IN ('ready','committed') AND d.grant_data->'binding'=b AND d.receipt=r
   AND d.grant_data->'restore'->>'capture_token'=b->>'snapshot_capture_token'
   AND d.grant_data->'restore'->'capture'->'parent'=d.grant_data->'source'->'parent'
   AND application_standard_restore_evidence_hash((b->>'snapshot_capture_token')::uuid,d.grant_data->'restore'->>'fc_version',d.grant_data->'restore'->'capture')=b->>'snapshot_evidence_hash'
  FOR SHARE NOWAIT;
 END IF;
 IF NOT FOUND OR NOT application_standard_snapshot_consumption_matches(r->'snapshot_consumption',b,captured,r->'artifact_consumption') THEN
  RAISE EXCEPTION 'native restore backing differs from catalog' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
END;
$$;
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
 IF c.instance_id IS NULL OR (c.node_id IS DISTINCT FROM i.node_id AND function_migration_runtime_receipt(i.id,i.node_id,i.wake_id) IS NULL) OR NOT application_standard_native_inputs_match(c.input_snapshot,input)
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
  'input_snapshot',input,'captured_input_hash',application_standard_native_input_hash(c.input_snapshot,i.node_id),
  'protocol_version',protocol,'node_id',i.node_id::text,'incarnation',incarnation::text,'clock_unix_nano',(extract(epoch FROM now_utc)*1000000000)::bigint);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$function$;

CREATE OR REPLACE FUNCTION public.application_standard_native_publication_guard()
 RETURNS trigger
 LANGUAGE plpgsql
AS $function$
DECLARE c instance_application_standard_admissions%ROWTYPE; g instance_application_standard_boots%ROWTYPE;
        p instance_application_standard_promotions%ROWTYPE;
        incarnation uuid; protocol smallint; b jsonb; r jsonb; managed boolean; publishing boolean; input jsonb; artifact_deadline timestamptz; d function_migration_destinations%ROWTYPE;
BEGIN
 IF NEW.app_id IS NULL OR NEW.kind<>'wake' OR NEW.state NOT IN ('waking','cold_booting','running','warm','migrating') THEN RETURN NEW; END IF;
 SELECT * INTO c FROM instance_application_standard_admissions WHERE instance_id=NEW.id;
 managed:=application_standard_runtime_requires_native(c.input_snapshot);
 IF NOT managed THEN RETURN NEW; END IF;
 publishing:=NEW.state IN ('running','warm','migrating') OR coalesce(NEW.netns,'')<>'' OR NEW.host_ip IS NOT NULL OR coalesce(NEW.guest_uid,0)>0;
 IF NOT publishing THEN RETURN NEW; END IF;
 SELECT * INTO d FROM function_migration_destinations WHERE instance_id=NEW.id AND node_id=NEW.node_id AND wake_id=NEW.wake_id AND phase='committed' FOR SHARE NOWAIT;
 IF FOUND THEN
  b:=d.grant_data->'binding'; r:=d.receipt;
  SELECT vmmd_incarnation,vmmd_admission_protocol INTO incarnation,protocol FROM compute_nodes WHERE id=NEW.node_id FOR SHARE NOWAIT;
  IF r IS NULL OR b->>'incarnation' IS DISTINCT FROM incarnation::text OR b->>'captured_input_hash' IS DISTINCT FROM application_standard_native_input_hash(c.input_snapshot,NEW.node_id)
   OR r->'binding' IS DISTINCT FROM b OR r->>'netns' IS DISTINCT FROM NEW.netns OR r->>'host_ip' IS DISTINCT FROM host(NEW.host_ip)
   OR r->>'lease_uid' IS DISTINCT FROM NEW.guest_uid::text OR r->>'paused' IS DISTINCT FROM 'false' OR NEW.state='warm' THEN
   RAISE EXCEPTION 'migration requires its exact native receipt' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
  PERFORM application_standard_native_artifact_protocol(b,r,c.input_snapshot,protocol);
  IF TG_OP='INSERT' OR OLD.node_id IS DISTINCT FROM NEW.node_id OR OLD.wake_id IS DISTINCT FROM NEW.wake_id THEN
   IF TG_OP='INSERT' OR OLD.state<>'migrating' OR OLD.node_id::text IS DISTINCT FROM d.grant_data->'source'->'parent'->'binding'->>'node_id'
    OR OLD.wake_id::text IS DISTINCT FROM d.grant_data->'source'->>'source_wake_id' OR OLD.lease_token IS DISTINCT FROM d.lease_token
    OR NEW.lease_token IS DISTINCT FROM d.lease_token OR NEW.state<>'running'
    OR NOT EXISTS (SELECT 1 FROM function_migration_sources s JOIN migration_leases m ON m.instance_id=s.instance_id AND m.lease_token=s.lease_token
     WHERE s.instance_id=NEW.id AND s.lease_token=d.lease_token AND s.phase='prepared' AND NOT m.pending AND m.lease_expires_at>clock_timestamp()
      AND s.capture_grant=d.grant_data->'source' AND s.capture_result=d.grant_data->'restore'->'capture') THEN
    RAISE EXCEPTION 'migration source ownership changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
   END IF;
   input:=application_standard_native_runtime_snapshot(NEW.app_id,NEW.deployment_id) || jsonb_build_object('instance_ram_mb',NEW.ram_mb,'instance_mode',NEW.mode);
   IF NOT application_standard_native_inputs_match(c.input_snapshot,input) THEN
    RAISE EXCEPTION 'migration runtime inputs changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
   END IF;
   artifact_deadline:=application_standard_native_artifact_deadline(input,clock_timestamp());
   IF (artifact_deadline IS NOT NULL AND (b->>'expires_at_unix_nano')::bigint>(extract(epoch FROM artifact_deadline)*1000000000)::bigint)
    OR (b->>'expires_at_unix_nano')::bigint<=(extract(epoch FROM clock_timestamp())*1000000000)::bigint THEN
    RAISE EXCEPTION 'migration authority expired before publication' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
   END IF;
  END IF;
  RETURN NEW;
 END IF;
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
 r:=coalesce(function_migration_runtime_receipt(i.id,i.node_id,i.wake_id),r);
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

-- +goose StatementEnd

-- Share generic and function migration capacity in this same transaction.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION function_migration_pending_node_usage(target uuid) RETURNS TABLE(ram bigint,cpu bigint,vcpu bigint)
LANGUAGE sql STABLE AS $$
 SELECT i.capacity_ram_mb::bigint,i.capacity_cpu_millicores::bigint,i.capacity_vcpu::bigint
 FROM function_migration_destinations d JOIN instances i ON i.id=d.instance_id
 WHERE d.node_id=target AND d.phase IN ('issued','ready')
 UNION ALL
 SELECT i.capacity_ram_mb::bigint,i.capacity_cpu_millicores::bigint,i.capacity_vcpu::bigint
 FROM function_migration_destinations d JOIN instances i ON i.id=d.instance_id
 JOIN function_migration_sources s ON s.instance_id=d.instance_id AND s.lease_token=d.lease_token
 WHERE s.source_node_id=target::text AND d.phase='committed' AND s.phase NOT IN ('recovered','retired')
$$;
CREATE OR REPLACE FUNCTION migration_source_node_usage(target uuid,legacy boolean DEFAULT false) RETURNS TABLE(ram bigint,cpu bigint,vcpu bigint) LANGUAGE sql AS $$
 SELECT original.ram+extra.ram,original.cpu+extra.cpu,original.vcpu+extra.vcpu
 FROM (
SELECT coalesce(sum(CASE WHEN i.id IS NULL THEN r.ram_mb ELSE greatest(0,r.ram_mb-greatest(i.ram_mb+p.overhead_mb,CASE WHEN NOT legacy OR owned.present THEN i.capacity_ram_mb ELSE 0 END)) END),0)::bigint,
  coalesce(sum(CASE WHEN i.id IS NULL THEN r.cpu_millicores ELSE greatest(0,r.cpu_millicores-greatest(coalesce(nullif(a.cpu_millicores,0),p.startup_cpu),CASE WHEN NOT legacy OR owned.present THEN i.capacity_cpu_millicores ELSE CASE WHEN i.startup_cpu_boost_until>now() THEN p.startup_cpu ELSE 0 END END)) END),0)::bigint,
  coalesce(sum(CASE WHEN i.id IS NULL THEN r.vcpu ELSE greatest(0,r.vcpu-i.capacity_vcpu) END),0)::bigint
 FROM migration_sources r CROSS JOIN service_capacity_policy p
 LEFT JOIN instances i ON r.phase='bound' AND i.id=r.instance_id AND i.node_id=r.source_node_id AND i.wake_id=r.source_wake_id
  AND (i.state IN ('waking','cold_booting','running','draining','warm') OR NOT legacy AND i.state IN ('snapshotting','migrating','frozen'))
 LEFT JOIN apps a ON a.id=i.app_id
 LEFT JOIN LATERAL (SELECT EXISTS(SELECT 1 FROM migration_admissions d WHERE d.phase='committed' AND d.instance_id=i.id AND d.destination_node_id=i.node_id AND d.destination_wake_id=i.wake_id) OR EXISTS(SELECT 1 FROM migration_sources s WHERE s.phase='resumed' AND s.instance_id=i.id AND s.source_node_id=i.node_id AND s.source_wake_id=i.wake_id) AS present) owned ON true
 WHERE p.singleton AND r.source_node_id=target AND r.phase IN ('bound','retiring')
 ) original(ram,cpu,vcpu) CROSS JOIN (
 SELECT coalesce(sum(ram),0)::bigint AS ram,coalesce(sum(cpu),0)::bigint AS cpu,coalesce(sum(vcpu),0)::bigint AS vcpu
 FROM function_migration_pending_node_usage(target)
 ) extra;
$$;
CREATE OR REPLACE FUNCTION function_migration_check_capacity(node uuid, extra_ram bigint, extra_cpu bigint, extra_vcpu bigint, exclude_id uuid) RETURNS void
 LANGUAGE plpgsql AS $$
DECLARE n compute_nodes%ROWTYPE; used_ram bigint; used_cpu bigint; used_vcpu bigint; p service_capacity_policy%ROWTYPE;
BEGIN
 PERFORM pg_advisory_xact_lock(1852793957,hashtext(node::text));
 SELECT * INTO n FROM compute_nodes WHERE id=node FOR SHARE NOWAIT;
 SELECT * INTO p FROM service_capacity_policy WHERE singleton;
 IF n.id IS NULL OR n.lifecycle<>'active' OR n.admission_ceiling_mb<=0 OR n.vpcpus<=0 OR n.vcpu_budget<=0 THEN
  RAISE EXCEPTION 'migration target is not sized and active' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_capacity';
 END IF;
 SELECT ram,cpu,vcpu INTO used_ram,used_cpu,used_vcpu FROM migration_admission_node_usage(node,exclude_id);
 IF used_ram+extra_ram>n.admission_ceiling_mb OR used_cpu+extra_cpu>n.vpcpus::bigint*1000*p.cpu_overcommit OR used_vcpu+extra_vcpu>n.vcpu_budget THEN
  RAISE EXCEPTION 'migration destination capacity is reserved' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_capacity';
 END IF;
END;
$$;
CREATE OR REPLACE FUNCTION function_migration_instance_capacity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state NOT IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating','frozen') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND OLD.node_id IS NOT DISTINCT FROM NEW.node_id AND OLD.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating','frozen')
  AND NEW.capacity_ram_mb<=OLD.capacity_ram_mb AND NEW.capacity_cpu_millicores<=OLD.capacity_cpu_millicores AND NEW.capacity_vcpu<=OLD.capacity_vcpu THEN RETURN NEW; END IF;
 -- Serialize before inspecting pending claims: the first destination may still
 -- be uncommitted, so testing EXISTS before taking this lock would miss it.
 PERFORM pg_advisory_xact_lock(1852793957,hashtext(NEW.node_id::text));
 IF EXISTS (SELECT 1 FROM function_migration_pending_node_usage(NEW.node_id)) THEN
  PERFORM function_migration_check_capacity(NEW.node_id,NEW.capacity_ram_mb,NEW.capacity_cpu_millicores,NEW.capacity_vcpu,NEW.id);
 END IF;
 RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION guard_function_migration_node() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (TG_OP='DELETE' OR NEW.lifecycle IN ('maintenance','retired')) AND EXISTS (
  SELECT 1 FROM function_migration_pending_node_usage(OLD.id)
 ) THEN
  RAISE EXCEPTION 'function migration cleanup retains node resources' USING ERRCODE='23514',CONSTRAINT='function_migration_node_pending';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS function_migration_node_pending ON compute_nodes;
CREATE TRIGGER function_migration_node_pending BEFORE DELETE OR UPDATE OF lifecycle ON compute_nodes
 FOR EACH ROW EXECUTE FUNCTION guard_function_migration_node();
-- +goose StatementEnd


-- +goose Down

-- +goose StatementBegin
DROP TRIGGER function_migration_node_pending ON compute_nodes;
DROP FUNCTION guard_function_migration_node();
CREATE OR REPLACE FUNCTION migration_source_node_usage(target uuid,legacy boolean DEFAULT false) RETURNS TABLE(ram bigint,cpu bigint,vcpu bigint) LANGUAGE sql AS $$
 SELECT coalesce(sum(CASE WHEN i.id IS NULL THEN r.ram_mb ELSE greatest(0,r.ram_mb-greatest(i.ram_mb+p.overhead_mb,CASE WHEN NOT legacy OR owned.present THEN i.capacity_ram_mb ELSE 0 END)) END),0)::bigint,
  coalesce(sum(CASE WHEN i.id IS NULL THEN r.cpu_millicores ELSE greatest(0,r.cpu_millicores-greatest(coalesce(nullif(a.cpu_millicores,0),p.startup_cpu),CASE WHEN NOT legacy OR owned.present THEN i.capacity_cpu_millicores ELSE CASE WHEN i.startup_cpu_boost_until>now() THEN p.startup_cpu ELSE 0 END END)) END),0)::bigint,
  coalesce(sum(CASE WHEN i.id IS NULL THEN r.vcpu ELSE greatest(0,r.vcpu-i.capacity_vcpu) END),0)::bigint
 FROM migration_sources r CROSS JOIN service_capacity_policy p
 LEFT JOIN instances i ON r.phase='bound' AND i.id=r.instance_id AND i.node_id=r.source_node_id AND i.wake_id=r.source_wake_id
  AND (i.state IN ('waking','cold_booting','running','draining','warm') OR NOT legacy AND i.state IN ('snapshotting','migrating','frozen'))
 LEFT JOIN apps a ON a.id=i.app_id
 LEFT JOIN LATERAL (SELECT EXISTS(SELECT 1 FROM migration_admissions d WHERE d.phase='committed' AND d.instance_id=i.id AND d.destination_node_id=i.node_id AND d.destination_wake_id=i.wake_id) OR EXISTS(SELECT 1 FROM migration_sources s WHERE s.phase='resumed' AND s.instance_id=i.id AND s.source_node_id=i.node_id AND s.source_wake_id=i.wake_id) AS present) owned ON true
 WHERE p.singleton AND r.source_node_id=target AND r.phase IN ('bound','retiring');
$$;
CREATE OR REPLACE FUNCTION function_migration_check_capacity(node uuid, extra_ram bigint, extra_cpu bigint, extra_vcpu bigint, exclude_id uuid) RETURNS void
 LANGUAGE plpgsql AS $$
DECLARE n compute_nodes%ROWTYPE; used_ram bigint; used_cpu bigint; used_vcpu bigint; p service_capacity_policy%ROWTYPE;
BEGIN
 PERFORM pg_advisory_xact_lock(1852793957,hashtext(node::text));
 SELECT * INTO n FROM compute_nodes WHERE id=node FOR SHARE NOWAIT;
 SELECT * INTO p FROM service_capacity_policy WHERE singleton;
 IF n.id IS NULL OR n.lifecycle<>'active' OR n.admission_ceiling_mb<=0 OR n.vpcpus<=0 OR n.vcpu_budget<=0 THEN
  RAISE EXCEPTION 'migration target is not sized and active' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_capacity';
 END IF;
 SELECT coalesce(sum(ram),0),coalesce(sum(cpu),0),coalesce(sum(vcpu),0) INTO used_ram,used_cpu,used_vcpu FROM (
  SELECT i.capacity_ram_mb ram,i.capacity_cpu_millicores cpu,i.capacity_vcpu vcpu FROM instances i
   WHERE i.node_id=node AND i.id IS DISTINCT FROM exclude_id AND i.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating','frozen')
  UNION ALL SELECT i.capacity_ram_mb,i.capacity_cpu_millicores,i.capacity_vcpu FROM function_migration_destinations d JOIN instances i ON i.id=d.instance_id
   WHERE d.node_id=node AND d.phase IN ('issued','ready')
 ) resident;
 IF used_ram+extra_ram>n.admission_ceiling_mb OR used_cpu+extra_cpu>n.vpcpus::bigint*1000*p.cpu_overcommit OR used_vcpu+extra_vcpu>n.vcpu_budget THEN
  RAISE EXCEPTION 'migration destination capacity is reserved' USING ERRCODE='23514',CONSTRAINT='function_migration_destination_capacity';
 END IF;
END;
$$;
CREATE OR REPLACE FUNCTION function_migration_instance_capacity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state NOT IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating','frozen') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND OLD.node_id IS NOT DISTINCT FROM NEW.node_id AND OLD.state IN ('waking','cold_booting','running','draining','warm','snapshotting','migrating','frozen')
  AND NEW.capacity_ram_mb<=OLD.capacity_ram_mb AND NEW.capacity_cpu_millicores<=OLD.capacity_cpu_millicores AND NEW.capacity_vcpu<=OLD.capacity_vcpu THEN RETURN NEW; END IF;
 -- Serialize before inspecting pending claims: the first destination may still
 -- be uncommitted, so testing EXISTS before taking this lock would miss it.
 PERFORM pg_advisory_xact_lock(1852793957,hashtext(NEW.node_id::text));
 IF EXISTS (SELECT 1 FROM function_migration_destinations WHERE node_id=NEW.node_id AND phase IN ('issued','ready')) THEN
  PERFORM function_migration_check_capacity(NEW.node_id,NEW.capacity_ram_mb,NEW.capacity_cpu_millicores,NEW.capacity_vcpu,NEW.id);
 END IF;
 RETURN NEW;
END;
$$;
DROP FUNCTION function_migration_pending_node_usage(uuid);
-- +goose StatementEnd


-- Preserve immutable committed epochs and their fail-closed native guards.
