-- +goose Up
CREATE TABLE instance_application_standard_promotions (
 token uuid PRIMARY KEY,
 instance_id uuid NOT NULL UNIQUE REFERENCES instances(id) ON DELETE CASCADE,
 parent_token uuid NOT NULL REFERENCES instance_application_standard_boots(token) ON DELETE CASCADE,
 binding jsonb NOT NULL CHECK(jsonb_typeof(binding)='object'),
 receipt jsonb CHECK(receipt IS NULL OR jsonb_typeof(receipt)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 received_at timestamptz,
 CHECK((receipt IS NULL)=(received_at IS NULL)),
 CHECK(token<>parent_token)
);
ALTER TABLE instances ADD COLUMN application_standard_promotion_token uuid REFERENCES instance_application_standard_promotions(token);

-- Retain the initial capture and boot receipt. A separate purpose owns each
-- fresh promotion attempt; history cannot be rewritten into resume authority.
-- +goose StatementBegin
CREATE FUNCTION application_standard_lock_native_promotion(instance_id uuid,allow_running boolean)
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION application_standard_native_promotion_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
     (parent->'binding'-ARRAY['token','payload_hash','issued_at_unix_nano','expires_at_unix_nano'])
  OR coalesce(b->>'payload_hash','') !~ '^[0-9a-f]{64}$'
  OR coalesce((b->>'issued_at_unix_nano')::bigint,0)<=0
  OR coalesce((b->>'expires_at_unix_nano')::bigint,0)<=now_nano
  OR (b->>'expires_at_unix_nano')::bigint <= (b->>'issued_at_unix_nano')::bigint
  OR (b->>'expires_at_unix_nano')::numeric-(b->>'issued_at_unix_nano')::numeric >600000000000
  OR (b->>'issued_at_unix_nano')::bigint>now_nano+5000000000
  OR (TG_OP='INSERT' AND (b->>'issued_at_unix_nano')::bigint<now_nano-5000000000) THEN
  RAISE EXCEPTION 'native promotion grant is stale or invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF TG_OP='UPDATE' THEN
  r:=NEW.receipt;
  IF r->'binding' IS DISTINCT FROM b OR (r->>'paused')::boolean IS DISTINCT FROM false
   OR (r-ARRAY['binding','paused','completed_at_unix_nano']) IS DISTINCT FROM (parent-ARRAY['binding','paused','completed_at_unix_nano'])
   OR coalesce((r->>'completed_at_unix_nano')::bigint,0)<(b->>'issued_at_unix_nano')::bigint-5000000000
   OR (r->>'completed_at_unix_nano')::bigint<(parent->>'completed_at_unix_nano')::bigint
   OR (r->>'completed_at_unix_nano')::bigint >= (b->>'expires_at_unix_nano')::bigint
   OR (r->>'completed_at_unix_nano')::bigint>now_nano+5000000000 OR NEW.received_at IS NULL THEN
   RAISE EXCEPTION 'native promotion receipt is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_native_promotion_guard BEFORE INSERT OR UPDATE OR DELETE ON instance_application_standard_promotions
 FOR EACH ROW EXECUTE FUNCTION application_standard_native_promotion_guard();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_native_publication_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE c instance_application_standard_admissions%ROWTYPE; g instance_application_standard_boots%ROWTYPE;
        p instance_application_standard_promotions%ROWTYPE;
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
 SELECT vmmd_incarnation INTO incarnation FROM compute_nodes WHERE id=NEW.node_id FOR SHARE NOWAIT;
 IF g.instance_id IS DISTINCT FROM NEW.id OR r IS NULL OR b->>'node_id' IS DISTINCT FROM NEW.node_id::text
  OR b->>'incarnation' IS DISTINCT FROM incarnation::text OR b->>'captured_input_hash' IS DISTINCT FROM c.native_input_hash
  OR r->'binding' IS DISTINCT FROM b OR r->>'netns' IS DISTINCT FROM NEW.netns OR r->>'host_ip' IS DISTINCT FROM host(NEW.host_ip)
  OR (r->>'lease_uid')::integer IS DISTINCT FROM NEW.guest_uid OR (r->>'paused')::boolean IS DISTINCT FROM (NEW.state='warm') THEN
  RAISE EXCEPTION 'managed runtime requires its exact native receipt' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_receipt';
 END IF;
 IF TG_OP='INSERT' OR OLD.state IN ('waking','cold_booting') OR OLD.application_standard_boot_token IS DISTINCT FROM NEW.application_standard_boot_token
  OR OLD.application_standard_promotion_token IS DISTINCT FROM NEW.application_standard_promotion_token THEN
  IF (b->>'expires_at_unix_nano')::bigint <= (extract(epoch FROM clock_timestamp())*1000000000)::bigint THEN
   RAISE EXCEPTION 'native runtime authority expired before publication' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 END IF;
 RETURN NEW;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'native publication inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
-- +goose StatementEnd
DROP TRIGGER application_standard_b_native_publication ON instances;
CREATE TRIGGER application_standard_b_native_publication BEFORE INSERT OR UPDATE OF node_id,state,netns,host_ip,guest_uid,application_standard_boot_token,application_standard_promotion_token ON instances
 FOR EACH ROW EXECUTE FUNCTION application_standard_native_publication_guard();

-- +goose Down
-- Preserve fail-closed authority and immutable history on downgrade.
