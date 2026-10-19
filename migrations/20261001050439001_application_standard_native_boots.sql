-- +goose Up
-- Native process registration is owned by vmmd startup; no inherited intent
-- query is introduced at the privileged runtime boundary.
ALTER TABLE compute_nodes ADD COLUMN vmmd_incarnation uuid;
ALTER TABLE instance_application_standard_admissions ADD COLUMN node_id uuid;
-- convert_to is STABLE for an arbitrary encoding argument. This wrapper pins
-- UTF8, so the digest depends only on its immutable stored JSON and UUID inputs.
-- +goose StatementBegin
CREATE FUNCTION application_standard_native_input_hash(input jsonb,node uuid)
RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE SET search_path=pg_catalog AS $$
 SELECT encode(sha256(convert_to(input::text || E'\n' || coalesce(node::text,''),'UTF8')),'hex');
$$;
-- +goose StatementEnd
ALTER TABLE instance_application_standard_admissions ADD COLUMN native_input_hash text
 GENERATED ALWAYS AS (application_standard_native_input_hash(input_snapshot,node_id)) STORED;

CREATE TABLE instance_application_standard_boots (
 token uuid PRIMARY KEY,
 instance_id uuid NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
 expected_state text NOT NULL CHECK (expected_state IN ('waking','cold_booting')),
 binding jsonb NOT NULL CHECK (jsonb_typeof(binding)='object'),
 receipt jsonb CHECK (receipt IS NULL OR jsonb_typeof(receipt)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 received_at timestamptz,
 CHECK ((receipt IS NULL) = (received_at IS NULL))
);
CREATE INDEX instance_application_standard_boots_instance ON instance_application_standard_boots(instance_id);
ALTER TABLE instances ADD COLUMN application_standard_boot_token uuid REFERENCES instance_application_standard_boots(token);

-- +goose StatementBegin
CREATE FUNCTION application_standard_native_runtime_snapshot(application_id uuid,artifact_id uuid)
RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE input jsonb; revision bigint;
BEGIN
 input:=application_standard_runtime_snapshot(application_id,artifact_id);
 SELECT egress_allowlist_revision INTO revision FROM apps WHERE id=application_id FOR SHARE NOWAIT;
 RETURN input || jsonb_build_object('egress_revision',revision);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_instance_runtime_capture() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.app_id IS NOT NULL AND NEW.kind='wake' AND NEW.state IN ('waking','cold_booting','running','warm','migrating') THEN
  INSERT INTO instance_application_standard_admissions(instance_id,app_id,deployment_id,node_id,input_snapshot)
  VALUES(NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,
   application_standard_native_runtime_snapshot(NEW.app_id,NEW.deployment_id) || jsonb_build_object('instance_ram_mb',NEW.ram_mb,'instance_mode',NEW.mode));
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- Child owners never wait for parents. Retain all input and registered node
-- fences until the caller commits its grant or native receipt + publication.
-- +goose StatementBegin
CREATE FUNCTION application_standard_lock_native_boot(instance_id uuid,expected_state text)
RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE i instances%ROWTYPE; c instance_application_standard_admissions%ROWTYPE;
        input jsonb; incarnation uuid;
BEGIN
 SELECT * INTO i FROM instances WHERE id=instance_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR i.state IS DISTINCT FROM expected_state OR i.kind<>'wake' OR i.app_id IS NULL THEN
  RAISE EXCEPTION 'runtime boot state changed' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_conflict';
 END IF;
 SELECT * INTO c FROM instance_application_standard_admissions WHERE instance_application_standard_admissions.instance_id=i.id FOR SHARE NOWAIT;
 input:=application_standard_native_runtime_snapshot(i.app_id,i.deployment_id) || jsonb_build_object('instance_ram_mb',i.ram_mb,'instance_mode',i.mode);
 IF c.instance_id IS NULL OR c.node_id IS DISTINCT FROM i.node_id OR c.input_snapshot IS DISTINCT FROM input
   OR (input->>'desired_revision')::bigint<=0 OR input->>'effective_hash'=''
   OR input->'desired_revision' IS DISTINCT FROM input->'persisted_revision'
   OR (input->'adoptions'='[]'::jsonb AND input->'materialized_fields'='[]'::jsonb) THEN
  RAISE EXCEPTION 'runtime admission inputs changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT vmmd_incarnation INTO incarnation FROM compute_nodes WHERE id=i.node_id FOR SHARE NOWAIT;
 IF incarnation IS NULL THEN
  RAISE EXCEPTION 'native process is not registered' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN jsonb_build_object('input_snapshot',input,'captured_input_hash',c.native_input_hash,
  'node_id',i.node_id::text,'incarnation',incarnation::text,'clock_unix_nano',(extract(epoch FROM clock_timestamp())*1000000000)::bigint);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION application_standard_native_boot_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
 IF (b->>'protocol_version')::integer IS DISTINCT FROM 1 OR b->>'token' IS DISTINCT FROM NEW.token::text
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
  OR (b->>'expires_at_unix_nano')::numeric - (b->>'issued_at_unix_nano')::numeric > 600000000000
  OR (b->>'issued_at_unix_nano')::bigint > now_nano+5000000000 THEN
  RAISE EXCEPTION 'native boot grant is stale or invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF TG_OP='INSERT' AND (b->>'issued_at_unix_nano')::bigint < now_nano-5000000000 THEN
  RAISE EXCEPTION 'native boot issue time is stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF TG_OP='UPDATE' THEN
  r:=NEW.receipt;
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
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_native_boot_guard BEFORE INSERT OR UPDATE OR DELETE ON instance_application_standard_boots
 FOR EACH ROW EXECUTE FUNCTION application_standard_native_boot_guard();

-- Existing runtime capture checks remain in force. The additional trigger
-- requires the exact registered native receipt on every managed publication.
-- +goose StatementBegin
CREATE FUNCTION application_standard_native_publication_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c instance_application_standard_admissions%ROWTYPE; g instance_application_standard_boots%ROWTYPE;
        incarnation uuid; b jsonb; r jsonb; managed boolean; publishing boolean;
BEGIN
 IF NEW.app_id IS NULL OR NEW.kind<>'wake' OR NEW.state NOT IN ('waking','cold_booting','running','warm','migrating') THEN RETURN NEW; END IF;
 SELECT * INTO c FROM instance_application_standard_admissions WHERE instance_id=NEW.id;
 managed:=coalesce(c.input_snapshot->'adoptions'<>'[]'::jsonb OR c.input_snapshot->'materialized_fields'<>'[]'::jsonb,false);
 IF NOT managed THEN RETURN NEW; END IF;
 publishing:=NEW.state IN ('running','warm','migrating') OR coalesce(NEW.netns,'')<>'' OR NEW.host_ip IS NOT NULL OR coalesce(NEW.guest_uid,0)>0;
 IF NOT publishing THEN RETURN NEW; END IF;
 SELECT * INTO g FROM instance_application_standard_boots WHERE token=NEW.application_standard_boot_token FOR SHARE NOWAIT;
 b:=g.binding; r:=g.receipt;
 SELECT vmmd_incarnation INTO incarnation FROM compute_nodes WHERE id=NEW.node_id FOR SHARE NOWAIT;
 IF g.instance_id IS DISTINCT FROM NEW.id OR r IS NULL OR b->>'node_id' IS DISTINCT FROM NEW.node_id::text
  OR b->>'incarnation' IS DISTINCT FROM incarnation::text OR b->>'captured_input_hash' IS DISTINCT FROM c.native_input_hash
  OR r->'binding' IS DISTINCT FROM b OR r->>'netns' IS DISTINCT FROM NEW.netns OR r->>'host_ip' IS DISTINCT FROM host(NEW.host_ip)
  OR (r->>'lease_uid')::integer IS DISTINCT FROM NEW.guest_uid
  OR (r->>'paused')::boolean IS DISTINCT FROM (NEW.state='warm') THEN
  RAISE EXCEPTION 'managed runtime requires its exact native receipt' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 IF TG_OP='INSERT' OR OLD.state IN ('waking','cold_booting') OR OLD.application_standard_boot_token IS DISTINCT FROM NEW.application_standard_boot_token THEN
  IF (b->>'expires_at_unix_nano')::bigint <= (extract(epoch FROM clock_timestamp())*1000000000)::bigint THEN
   RAISE EXCEPTION 'native boot authority expired before publication' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 END IF;
 RETURN NEW;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'native publication inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_b_native_publication BEFORE INSERT OR UPDATE OF node_id,state,netns,host_ip,guest_uid,application_standard_boot_token ON instances
 FOR EACH ROW EXECUTE FUNCTION application_standard_native_publication_guard();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_instance_runtime_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_input jsonb; captured_input jsonb;
BEGIN
 IF TG_OP='UPDATE' AND OLD.kind='wake' AND OLD.app_id IS NOT NULL AND
   (NEW.app_id IS DISTINCT FROM OLD.app_id OR NEW.deployment_id IS DISTINCT FROM OLD.deployment_id OR NEW.kind IS DISTINCT FROM OLD.kind) THEN
  RAISE EXCEPTION 'runtime application and artifact identity are immutable'
   USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_identity';
 END IF;
 IF NEW.app_id IS NULL OR NEW.kind <> 'wake' THEN RETURN NEW; END IF;
 -- Cleanup, bookkeeping and nonresident fixtures remain possible while the
 -- standard is pending. Entry into boot, serving, warm and migration is gated.
 IF NEW.state NOT IN ('waking','cold_booting','running','warm','migrating') THEN RETURN NEW; END IF;
 current_input := application_standard_native_runtime_snapshot(NEW.app_id,NEW.deployment_id) ||
   jsonb_build_object('instance_ram_mb',NEW.ram_mb,'instance_mode',NEW.mode);
 IF TG_OP='INSERT' THEN
  IF NEW.state IN ('running','warm','migrating') AND
    (current_input->'adoptions' <> '[]'::jsonb OR current_input->'materialized_fields' <> '[]'::jsonb) THEN
   RAISE EXCEPTION 'managed runtime requires a captured boot attempt'
    USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 ELSE
  SELECT input_snapshot INTO captured_input FROM instance_application_standard_admissions WHERE instance_id=NEW.id;
  IF captured_input IS NULL THEN
   -- A legacy instance can continue only while it is still unmanaged. A new
   -- standard requires a fresh instance, never a fabricated historical capture.
   IF current_input->'adoptions' <> '[]'::jsonb OR current_input->'materialized_fields' <> '[]'::jsonb THEN
    RAISE EXCEPTION 'managed runtime has no admission capture'
     USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
   END IF;
  ELSE
   -- Legacy unmanaged captures have no native revision; they gain no native
   -- grant authority from this compatibility comparison.
   IF NOT (captured_input ? 'egress_revision') AND current_input->'adoptions'='[]'::jsonb AND current_input->'materialized_fields'='[]'::jsonb THEN
    current_input:=current_input-'egress_revision';
   END IF;
   IF captured_input IS DISTINCT FROM current_input THEN
   RAISE EXCEPTION 'runtime inputs changed after admission'
    USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
   END IF;
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_runtime_capture_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND pg_trigger_depth()>1 THEN RETURN NEW; END IF;
 -- Stored generated hashes are computed after BEFORE triggers. Compare only
 -- their immutable source columns here, including the captured node identity.
 IF TG_OP='UPDATE' AND NEW.instance_id IS NOT DISTINCT FROM OLD.instance_id AND NEW.app_id IS NOT DISTINCT FROM OLD.app_id
  AND NEW.deployment_id IS NOT DISTINCT FROM OLD.deployment_id AND NEW.node_id IS NOT DISTINCT FROM OLD.node_id
  AND NEW.input_snapshot IS NOT DISTINCT FROM OLD.input_snapshot AND NEW.captured_at IS NOT DISTINCT FROM OLD.captured_at THEN RETURN NEW; END IF;
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM instances WHERE id=OLD.instance_id) THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'runtime admission capture is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_capture_immutable';
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- Retain the receipt history and its fail-closed guards. A schema downgrade
-- cannot turn an already managed runtime back into unverified authority.
