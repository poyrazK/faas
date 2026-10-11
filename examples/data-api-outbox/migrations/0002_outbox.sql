CREATE TABLE api.outbox (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  order_id bigint NOT NULL UNIQUE REFERENCES api.orders(id),
  owner text NOT NULL,
  tenant text NOT NULL,
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  available_at timestamptz NOT NULL DEFAULT now(),
  lease_token uuid,
  lease_until timestamptz,
  delivered boolean NOT NULL DEFAULT false,
  CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);
ALTER TABLE api.outbox ENABLE ROW LEVEL SECURITY;
CREATE POLICY visible_events ON api.outbox FOR SELECT USING
  (owner=current_setting('request.jwt.claims',true)::jsonb->>'sub'
   AND tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant');
CREATE POLICY create_events ON api.outbox FOR INSERT WITH CHECK
  (owner=current_setting('request.jwt.claims',true)::jsonb->>'sub'
   AND tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant');
CREATE POLICY worker_events ON api.outbox FOR UPDATE USING
  (owner=current_setting('request.jwt.claims',true)::jsonb->>'sub'
   AND tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant'
   AND current_setting('request.jwt.claims',true)::jsonb->>'outbox_worker'='true')
  WITH CHECK (owner=current_setting('request.jwt.claims',true)::jsonb->>'sub'
   AND tenant=current_setting('request.jwt.claims',true)::jsonb->>'tenant'
   AND current_setting('request.jwt.claims',true)::jsonb->>'outbox_worker'='true');

CREATE FUNCTION api.validate_outbox() RETURNS trigger
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF TG_OP='INSERT' THEN
    IF NEW.attempts<>0 OR NEW.delivered OR NEW.lease_token IS NOT NULL
       OR NOT EXISTS(SELECT 1 FROM api.orders o WHERE o.id=NEW.order_id AND o.owner=NEW.owner AND o.tenant=NEW.tenant) THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_event';
    END IF;
  ELSIF NEW.id<>OLD.id OR NEW.order_id<>OLD.order_id OR NEW.owner<>OLD.owner OR NEW.tenant<>OLD.tenant THEN
    RAISE SQLSTATE 'PT409' USING MESSAGE='outbox_identity_immutable';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER validate_event BEFORE INSERT OR UPDATE ON api.outbox FOR EACH ROW EXECUTE FUNCTION api.validate_outbox();
CREATE FUNCTION api.enqueue_order() RETURNS trigger
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  INSERT INTO api.outbox(order_id,owner,tenant) VALUES(NEW.id,NEW.owner,NEW.tenant);
  RETURN NEW;
END;
$$;
CREATE TRIGGER enqueue_order AFTER INSERT ON api.orders FOR EACH ROW EXECUTE FUNCTION api.enqueue_order();
REVOKE EXECUTE ON FUNCTION api.validate_outbox(), api.enqueue_order() FROM PUBLIC;
CREATE INDEX outbox_pending ON api.outbox(owner,tenant,available_at,id) WHERE NOT delivered;

CREATE FUNCTION api.claim_event(claim_token uuid) RETURNS SETOF api.outbox
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF claim_token IS NULL OR COALESCE(current_setting('request.jwt.claims',true)::jsonb->>'outbox_worker','false')<>'true' THEN
    RAISE SQLSTATE 'PT403' USING MESSAGE='outbox_worker_required';
  END IF;
  RETURN QUERY WITH candidate AS (
    SELECT e.id FROM api.outbox e WHERE NOT e.delivered AND e.available_at<=clock_timestamp()
      AND (e.lease_until IS NULL OR e.lease_until<=clock_timestamp())
    ORDER BY e.available_at,e.id FOR UPDATE SKIP LOCKED LIMIT 1
  ) UPDATE api.outbox e SET attempts=e.attempts+1, lease_token=claim_token,
    lease_until=clock_timestamp()+interval '30 seconds'
    FROM candidate c WHERE e.id=c.id RETURNING e.*;
END;
$$;
CREATE FUNCTION api.finish_event(event_id bigint, claim_token uuid, succeeded boolean) RETURNS SETOF api.outbox
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF claim_token IS NULL OR succeeded IS NULL OR COALESCE(current_setting('request.jwt.claims',true)::jsonb->>'outbox_worker','false')<>'true' THEN
    RAISE SQLSTATE 'PT403' USING MESSAGE='outbox_worker_required';
  END IF;
  RETURN QUERY UPDATE api.outbox e SET delivered=succeeded, lease_token=NULL, lease_until=NULL,
    available_at=clock_timestamp()+make_interval(secs=>LEAST(300, power(2,LEAST(e.attempts,8))::integer))
    WHERE e.id=event_id AND e.lease_token=claim_token AND e.lease_until>clock_timestamp() AND NOT e.delivered
    RETURNING e.*;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.claim_event(uuid),api.finish_event(bigint,uuid,boolean) FROM PUBLIC;
COMMENT ON FUNCTION api.claim_event(uuid) IS '@gregale:rpc';
COMMENT ON FUNCTION api.finish_event(bigint,uuid,boolean) IS '@gregale:rpc';
