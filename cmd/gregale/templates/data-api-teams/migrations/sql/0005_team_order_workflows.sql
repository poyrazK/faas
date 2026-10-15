-- Updates follow draft -> submitted -> fulfilled; imported initial states
-- remain supported for historical data. Every later update obeys these rules.
CREATE TABLE api.team_order_events (
  tenant_id text NOT NULL,
  order_id uuid NOT NULL,
  version integer NOT NULL CHECK(version>1),
  actor text NOT NULL,
  action text NOT NULL CHECK(action IN ('edit','submit','fulfil')),
  before_status api.order_status NOT NULL,
  after_status api.order_status NOT NULL,
  total_cents integer NOT NULL CHECK(total_cents>=0),
  occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY(tenant_id,order_id,version)
);
ALTER TABLE api.team_order_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY team_order_history_reads ON api.team_order_events FOR SELECT USING (
  EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_order_events.tenant_id
    AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')
);
CREATE POLICY reviewed_order_history_creates ON api.team_order_events FOR INSERT WITH CHECK (
  current_setting('gregale.team_order_write',true)='on'
  AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub'
  AND EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_order_events.tenant_id AND m.role IN ('owner','admin'))
);
CREATE FUNCTION api.guard_team_order_workflow() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.version<>OLD.version+1 THEN
    RAISE SQLSTATE 'PT409' USING MESSAGE='invalid_order_revision';
  END IF;
  IF NEW.status=OLD.status THEN
    IF OLD.status<>'draft' THEN RAISE SQLSTATE 'PT409' USING MESSAGE='order_not_editable'; END IF;
  ELSE
    IF current_setting('gregale.order_transition',true) IS DISTINCT FROM NEW.status::text
      OR NOT ((OLD.status='draft' AND NEW.status='submitted') OR (OLD.status='submitted' AND NEW.status='fulfilled'))
      OR NEW.total_cents<>OLD.total_cents THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE='invalid_order_transition';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.guard_team_order_workflow() FROM PUBLIC;
CREATE TRIGGER guard_team_order_workflow BEFORE UPDATE ON api.team_orders FOR EACH ROW EXECUTE FUNCTION api.guard_team_order_workflow();
CREATE FUNCTION api.record_team_order_event() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE caller_subject text:=NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
BEGIN
  IF caller_subject IS NOT NULL AND current_setting('gregale.team_order_write',true)='on' THEN
    INSERT INTO api.team_order_events(tenant_id,order_id,version,actor,action,before_status,after_status,total_cents)
    VALUES(NEW.tenant_id,NEW.id,NEW.version,caller_subject,
      CASE WHEN NEW.status=OLD.status THEN 'edit' WHEN NEW.status='submitted' THEN 'submit' ELSE 'fulfil' END,
      OLD.status,NEW.status,NEW.total_cents);
  END IF;
  RETURN NEW;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.record_team_order_event() FROM PUBLIC;
CREATE TRIGGER record_team_order_event AFTER UPDATE ON api.team_orders FOR EACH ROW EXECUTE FUNCTION api.record_team_order_event();

CREATE OR REPLACE FUNCTION api.save_team_order_once(request_key uuid, team_id text,
  order_id uuid, expected_version integer, order_total integer,
  order_state api.order_status)
RETURNS SETOF api.team_orders LANGUAGE plpgsql SECURITY INVOKER
SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  actor text := NULLIF(current_setting('request.jwt.claims', true)::jsonb ->> 'sub', '');
  previous_scope text := current_setting('gregale.team_order_write', true);
  intended jsonb;
  saved api.team_order_receipts;
  result api.team_orders;
BEGIN
  IF actor IS NULL OR request_key IS NULL OR team_id IS NULL OR team_id = ''
    OR order_id IS NULL OR expected_version IS NULL OR expected_version < 0
    OR order_total IS NULL OR order_total < 0 OR order_state IS NULL THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'invalid_order';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM api.team_members m WHERE m.tenant_id = team_id AND m.role IN ('owner', 'admin')) THEN
    RAISE SQLSTATE '42501' USING MESSAGE = 'team_write_denied';
  END IF;
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='order_read_committed_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=actor AND m.role IN ('owner','admin')) THEN
    RAISE SQLSTATE '42501' USING MESSAGE='team_write_denied';
  END IF;
  intended := jsonb_build_object('id', order_id, 'version', expected_version,
    'total', order_total, 'status', order_state);
  PERFORM pg_advisory_xact_lock(hashtextextended(actor || ':' || team_id || ':' || request_key::text, 0));
  PERFORM set_config('gregale.team_order_write', 'on', true);
  SELECT r.* INTO saved FROM api.team_order_receipts r
    WHERE r.tenant_id = team_id AND r.subject = actor AND r.request_key = save_team_order_once.request_key;
  IF FOUND THEN
    IF saved.payload <> intended THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict';
    END IF;
    SELECT * INTO result FROM jsonb_populate_record(NULL::api.team_orders, saved.response);
  ELSE
    IF expected_version = 0 THEN
      IF order_state<>'draft' THEN RAISE SQLSTATE 'PT409' USING MESSAGE='invalid_order_transition'; END IF;
      INSERT INTO api.team_orders(id, tenant_id, created_by, total_cents, status)
        VALUES(order_id, team_id, actor, order_total, order_state) RETURNING * INTO result;
    ELSE
      UPDATE api.team_orders o SET total_cents = order_total, status = order_state, version = o.version + 1
        WHERE o.id = order_id AND o.tenant_id = team_id AND o.version = expected_version RETURNING o.* INTO result;
      IF NOT FOUND THEN
        RAISE SQLSTATE 'PT409' USING MESSAGE = 'order_version_conflict';
      END IF;
    END IF;
    INSERT INTO api.team_order_receipts(tenant_id, subject, request_key, payload, response)
      VALUES(team_id, actor, request_key, intended, to_jsonb(result));
  END IF;
  PERFORM set_config('gregale.team_order_write', COALESCE(previous_scope, ''), true);
  RETURN NEXT result;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.team_order_write', COALESCE(previous_scope, ''), true);
  RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.save_team_order_once(uuid,text,uuid,integer,integer,api.order_status) FROM PUBLIC;
COMMENT ON FUNCTION api.save_team_order_once(uuid,text,uuid,integer,integer,api.order_status) IS '@gregale:rpc';

CREATE FUNCTION api.transition_team_order_once(request_key uuid,team_id text,order_id uuid,expected_version integer,next_state api.order_status)
RETURNS SETOF api.team_orders LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text:=NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
  old_write text:=current_setting('gregale.team_order_write',true);
  old_transition text:=current_setting('gregale.order_transition',true);
  receipt api.team_order_receipts;
  result api.team_orders;
  intended jsonb;
BEGIN
  IF caller_subject IS NULL OR request_key IS NULL OR team_id IS NULL OR team_id='' OR order_id IS NULL OR expected_version IS NULL OR expected_version<1 OR next_state IS NULL OR next_state NOT IN ('submitted','fulfilled') THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_action';
  END IF;
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='order_read_committed_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject AND m.role IN ('owner','admin')) THEN RAISE SQLSTATE '42501' USING MESSAGE='team_write_denied'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended(caller_subject || ':' || team_id || ':' || request_key::text,0));
  PERFORM set_config('gregale.team_order_write','on',true);
  intended:=jsonb_build_object('kind','transition','id',order_id,'version',expected_version,'state',next_state);
  SELECT r.* INTO receipt FROM api.team_order_receipts r WHERE r.tenant_id=team_id AND r.subject=caller_subject AND r.request_key=transition_team_order_once.request_key;
  IF FOUND THEN
    IF receipt.payload<>intended THEN RAISE SQLSTATE 'PT409' USING MESSAGE='idempotency_payload_conflict'; END IF;
    SELECT * INTO result FROM jsonb_populate_record(NULL::api.team_orders,receipt.response);
  ELSE
    PERFORM set_config('gregale.order_transition',next_state::text,true);
    UPDATE api.team_orders o SET status=next_state,version=o.version+1
      WHERE o.tenant_id=team_id AND o.id=order_id AND o.version=expected_version RETURNING o.* INTO result;
    IF NOT FOUND THEN RAISE SQLSTATE 'PT409' USING MESSAGE='order_version_conflict'; END IF;
    INSERT INTO api.team_order_receipts(tenant_id,subject,request_key,payload,response) VALUES(team_id,caller_subject,request_key,intended,to_jsonb(result));
  END IF;
  PERFORM set_config('gregale.team_order_write',COALESCE(old_write,''),true);
  PERFORM set_config('gregale.order_transition',COALESCE(old_transition,''),true);
  RETURN NEXT result;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.team_order_write',COALESCE(old_write,''),true);
  PERFORM set_config('gregale.order_transition',COALESCE(old_transition,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.transition_team_order_once(uuid,text,uuid,integer,api.order_status) FROM PUBLIC;
COMMENT ON FUNCTION api.transition_team_order_once(uuid,text,uuid,integer,api.order_status) IS '@gregale:rpc';
