-- filename: 20261023084252389_application_standard_rebinds.sql
-- migration-compat: expand
-- ADR-595 extension (docs/adr/20261018-runtime-admission-rebind.md), slice 1:
-- an admitted runtime survives a vmmd restart. One append-only row records
-- that an instance's admission, issued under incarnation A, is held by the
-- node's current incarnation B since ownership epoch E. schedd writes it
-- from the successor's adoption report. Receipts and bindings stay
-- immutable; the guards below resolve a receipt's issuing incarnation
-- through these rows. With no rows every guard behaves exactly as before.
-- Promotion's fresh-versus-parent comparison stays strict until slice 2
-- carries the re-bind on the wire.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS instance_application_standard_rebinds (
 instance_id uuid NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
 node_id uuid NOT NULL,
 from_incarnation uuid NOT NULL CHECK (from_incarnation <> '00000000-0000-0000-0000-000000000000'::uuid),
 to_incarnation uuid NOT NULL CHECK (to_incarnation <> '00000000-0000-0000-0000-000000000000'::uuid),
 vmmd_epoch bigint NOT NULL CHECK (vmmd_epoch > 0),
 observed jsonb NOT NULL CHECK (jsonb_typeof(observed) = 'object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (instance_id, from_incarnation),
 UNIQUE (instance_id, to_incarnation),
 CHECK (to_incarnation <> from_incarnation),
 FOREIGN KEY (node_id, from_incarnation) REFERENCES application_standard_native_incarnations (node_id, incarnation) ON DELETE CASCADE,
 FOREIGN KEY (node_id, to_incarnation) REFERENCES application_standard_native_incarnations (node_id, incarnation) ON DELETE CASCADE
);

-- The incarnation an admission issued under `issued` is held by now: follow
-- the instance's re-binds. Rows never change and each one moves to a later
-- registration, so the walk is acyclic and short.
CREATE OR REPLACE FUNCTION application_standard_effective_incarnation(instance uuid, issued text) RETURNS text
LANGUAGE plpgsql STABLE AS $$
DECLARE held text := issued; nxt uuid; hops integer := 0;
BEGIN
 IF held IS NULL OR held !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN RETURN held; END IF;
 LOOP
  SELECT to_incarnation INTO nxt FROM instance_application_standard_rebinds
   WHERE instance_id = instance AND from_incarnation = held::uuid;
  EXIT WHEN NOT FOUND OR hops >= 4096;
  held := nxt::text; hops := hops + 1;
 END LOOP;
 RETURN held;
END;
$$;

-- Append-only, and only the node's current holder may extend an
-- admission's chain, for a live instance whose residency equals its receipt.
CREATE OR REPLACE FUNCTION application_standard_rebind_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE n compute_nodes%ROWTYPE; i instances%ROWTYPE; g instance_application_standard_boots%ROWTYPE;
        p instance_application_standard_promotions%ROWTYPE; r jsonb; from_at timestamptz; to_at timestamptz;
BEGIN
 IF TG_OP = 'DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM instances WHERE id = OLD.instance_id)
   OR NOT EXISTS(SELECT 1 FROM application_standard_native_incarnations h WHERE h.node_id = OLD.node_id AND h.incarnation = OLD.to_incarnation) THEN
   RETURN OLD;
  END IF;
  RAISE EXCEPTION 'runtime re-bind history is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 IF TG_OP = 'UPDATE' THEN
  RAISE EXCEPTION 'runtime re-bind history is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 SELECT * INTO n FROM compute_nodes WHERE id = NEW.node_id FOR SHARE NOWAIT;
 IF NOT FOUND OR n.vmmd_incarnation IS DISTINCT FROM NEW.to_incarnation
  OR n.vmmd_epoch_incarnation IS DISTINCT FROM NEW.to_incarnation OR n.vmmd_epoch IS DISTINCT FROM NEW.vmmd_epoch THEN
  RAISE EXCEPTION 'runtime re-bind must name the node''s current holder' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT * INTO i FROM instances WHERE id = NEW.instance_id FOR SHARE NOWAIT;
 IF NOT FOUND OR i.node_id IS DISTINCT FROM NEW.node_id OR i.state NOT IN ('running','warm') OR i.application_standard_boot_token IS NULL THEN
  RAISE EXCEPTION 'runtime re-bind requires a live admitted instance on its node' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT * INTO g FROM instance_application_standard_boots WHERE token = i.application_standard_boot_token FOR SHARE NOWAIT;
 r := g.receipt;
 IF i.application_standard_promotion_token IS NOT NULL THEN
  SELECT * INTO p FROM instance_application_standard_promotions WHERE token = i.application_standard_promotion_token FOR SHARE NOWAIT;
  IF NOT FOUND OR p.instance_id IS DISTINCT FROM i.id OR p.parent_token IS DISTINCT FROM g.token THEN
   RAISE EXCEPTION 'runtime re-bind promotion changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  r := p.receipt;
 END IF;
 IF g.instance_id IS DISTINCT FROM i.id OR r IS NULL
  OR application_standard_effective_incarnation(i.id, r->'binding'->>'incarnation') IS DISTINCT FROM NEW.from_incarnation::text THEN
  RAISE EXCEPTION 'runtime re-bind must extend the admission''s current holder' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT registered_at INTO from_at FROM application_standard_native_incarnations WHERE node_id = NEW.node_id AND incarnation = NEW.from_incarnation;
 SELECT registered_at INTO to_at FROM application_standard_native_incarnations WHERE node_id = NEW.node_id AND incarnation = NEW.to_incarnation;
 IF from_at IS NULL OR to_at IS NULL OR from_at >= to_at THEN
  RAISE EXCEPTION 'runtime re-bind must move to a later incarnation' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF (SELECT count(*) FROM jsonb_object_keys(NEW.observed)) <> 6
  OR NEW.observed->>'token' IS DISTINCT FROM r->'binding'->>'token'
  OR NEW.observed->>'completed_at_unix_nano' IS DISTINCT FROM r->>'completed_at_unix_nano'
  OR NEW.observed->>'netns' IS DISTINCT FROM r->>'netns' OR i.netns IS DISTINCT FROM r->>'netns'
  OR NEW.observed->>'host_ip' IS DISTINCT FROM r->>'host_ip' OR host(i.host_ip) IS DISTINCT FROM r->>'host_ip'
  OR NEW.observed->>'lease_uid' IS DISTINCT FROM r->>'lease_uid' OR i.guest_uid::text IS DISTINCT FROM r->>'lease_uid'
  OR NEW.observed->'paused' IS DISTINCT FROM r->'paused' OR (r->>'paused')::boolean IS DISTINCT FROM (i.state = 'warm') THEN
  RAISE EXCEPTION 'runtime re-bind residency differs from its receipt' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 NEW.created_at := clock_timestamp();
 RETURN NEW;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime re-bind inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
CREATE OR REPLACE TRIGGER application_standard_rebind_guard BEFORE INSERT OR UPDATE OR DELETE ON instance_application_standard_rebinds
 FOR EACH ROW EXECUTE FUNCTION application_standard_rebind_guard();

CREATE OR REPLACE FUNCTION application_standard_native_publication_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
  OR application_standard_effective_incarnation(NEW.id,b->>'incarnation') IS DISTINCT FROM incarnation::text OR b->>'captured_input_hash' IS DISTINCT FROM c.native_input_hash
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

CREATE OR REPLACE FUNCTION application_standard_lock_native_promotion(instance_id uuid,allow_running boolean)
RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE i instances%ROWTYPE; g instance_application_standard_boots%ROWTYPE; locked jsonb; b jsonb; r jsonb;
BEGIN
 SELECT * INTO i FROM instances WHERE id=instance_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR (i.state<>'warm' AND NOT(allow_running AND i.state='running')) THEN
  RAISE EXCEPTION 'runtime promotion state changed' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_conflict';
 END IF;
 locked:=application_standard_lock_native_boot(i.id,i.state);
 SELECT * INTO g FROM instance_application_standard_boots WHERE token=i.application_standard_boot_token FOR SHARE NOWAIT;
 b:=g.binding; r:=g.receipt;
 IF g.instance_id IS DISTINCT FROM i.id OR r IS NULL OR (r->>'paused')::boolean IS DISTINCT FROM true
  OR b->>'node_id' IS DISTINCT FROM locked->>'node_id' OR application_standard_effective_incarnation(i.id,b->>'incarnation') IS DISTINCT FROM locked->>'incarnation'
  OR b->>'captured_input_hash' IS DISTINCT FROM locked->>'captured_input_hash'
  OR r->>'netns' IS DISTINCT FROM i.netns OR r->>'host_ip' IS DISTINCT FROM host(i.host_ip)
  OR (r->>'lease_uid')::integer IS DISTINCT FROM i.guest_uid
  OR (i.state='warm' AND i.application_standard_promotion_token IS NOT NULL) THEN
  RAISE EXCEPTION 'paused native lease changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN locked || jsonb_build_object('parent',r,'state',i.state,'promotion_token',i.application_standard_promotion_token);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime promotion inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_lock_snapshot_capture(instance_id uuid, expected_state text) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
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
  OR b->>'node_id' IS DISTINCT FROM i.node_id::text OR application_standard_effective_incarnation(i.id,b->>'incarnation') IS DISTINCT FROM locked->>'incarnation'
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

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION application_standard_native_publication_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
$$;

CREATE OR REPLACE FUNCTION application_standard_lock_native_promotion(instance_id uuid,allow_running boolean)
RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE i instances%ROWTYPE; g instance_application_standard_boots%ROWTYPE; locked jsonb; b jsonb; r jsonb;
BEGIN
 SELECT * INTO i FROM instances WHERE id=instance_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR (i.state<>'warm' AND NOT(allow_running AND i.state='running')) THEN
  RAISE EXCEPTION 'runtime promotion state changed' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_conflict';
 END IF;
 locked:=application_standard_lock_native_boot(i.id,i.state);
 SELECT * INTO g FROM instance_application_standard_boots WHERE token=i.application_standard_boot_token FOR SHARE NOWAIT;
 b:=g.binding; r:=g.receipt;
 IF g.instance_id IS DISTINCT FROM i.id OR r IS NULL OR (r->>'paused')::boolean IS DISTINCT FROM true
  OR b->>'node_id' IS DISTINCT FROM locked->>'node_id' OR b->>'incarnation' IS DISTINCT FROM locked->>'incarnation'
  OR b->>'captured_input_hash' IS DISTINCT FROM locked->>'captured_input_hash'
  OR r->>'netns' IS DISTINCT FROM i.netns OR r->>'host_ip' IS DISTINCT FROM host(i.host_ip)
  OR (r->>'lease_uid')::integer IS DISTINCT FROM i.guest_uid
  OR (i.state='warm' AND i.application_standard_promotion_token IS NOT NULL) THEN
  RAISE EXCEPTION 'paused native lease changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN locked || jsonb_build_object('parent',r,'state',i.state,'promotion_token',i.application_standard_promotion_token);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime promotion inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_lock_snapshot_capture(instance_id uuid, expected_state text) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
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
DROP TRIGGER application_standard_rebind_guard ON instance_application_standard_rebinds;
DROP FUNCTION application_standard_rebind_guard();
DROP FUNCTION application_standard_effective_incarnation(uuid, text);
DROP TABLE instance_application_standard_rebinds;
-- +goose StatementEnd
