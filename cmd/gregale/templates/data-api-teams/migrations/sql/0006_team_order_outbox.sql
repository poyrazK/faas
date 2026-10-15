CREATE TYPE api.delivery_state AS ENUM ('pending','leased','delivered','dead');
CREATE TABLE api.team_order_outbox (
  event_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id text NOT NULL,
  order_id uuid NOT NULL,
  version integer NOT NULL,
  payload jsonb NOT NULL,
  state api.delivery_state NOT NULL DEFAULT 'pending',
  attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
  total_attempts integer NOT NULL DEFAULT 0 CHECK(total_attempts>=attempts),
  generation integer NOT NULL DEFAULT 0 CHECK(generation>=0),
  available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  lease_token uuid,
  lease_until timestamptz,
  delivered_at timestamptz,
  UNIQUE(tenant_id,order_id,version),
  UNIQUE(tenant_id,lease_token),
  CHECK((state='leased')=(lease_token IS NOT NULL AND lease_until IS NOT NULL)),
  CHECK((lease_token IS NULL)=(lease_until IS NULL)),
  CHECK((state='delivered')=(delivered_at IS NOT NULL))
);
CREATE INDEX team_outbox_pending ON api.team_order_outbox(tenant_id,available_at,event_id) WHERE state IN ('pending','leased');
CREATE TABLE api.team_order_delivery_attempts (
  event_id uuid NOT NULL REFERENCES api.team_order_outbox(event_id),
  tenant_id text NOT NULL,
  attempt integer NOT NULL CHECK(attempt>0),
  started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  finished_at timestamptz,
  outcome text NOT NULL DEFAULT 'leased' CHECK(outcome IN ('leased','delivered','failed','expired')),
  http_status integer CHECK(http_status BETWEEN 100 AND 599),
  PRIMARY KEY(event_id,attempt),
  CHECK((outcome='leased')=(finished_at IS NULL))
);
ALTER TABLE api.team_order_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE api.team_order_delivery_attempts ENABLE ROW LEVEL SECURITY;
CREATE POLICY owner_worker_reads ON api.team_order_outbox FOR SELECT USING ((EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_order_outbox.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner')) OR (current_setting('request.jwt.claims',true)::jsonb->'outbox_worker'='true'::jsonb AND current_setting('request.jwt.claims',true)::jsonb->>'outbox_team'=tenant_id));
CREATE POLICY scoped_queue_updates ON api.team_order_outbox FOR UPDATE USING (tenant_id=current_setting('gregale.outbox_write',true)) WITH CHECK(tenant_id=current_setting('gregale.outbox_write',true));
CREATE POLICY owner_worker_reads ON api.team_order_delivery_attempts FOR SELECT USING ((EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_order_delivery_attempts.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner')) OR (current_setting('request.jwt.claims',true)::jsonb->'outbox_worker'='true'::jsonb AND current_setting('request.jwt.claims',true)::jsonb->>'outbox_team'=tenant_id));
CREATE POLICY scoped_queue_updates ON api.team_order_delivery_attempts FOR UPDATE USING (tenant_id=current_setting('gregale.outbox_write',true)) WITH CHECK(tenant_id=current_setting('gregale.outbox_write',true));
CREATE POLICY reviewed_queue_creates ON api.team_order_outbox FOR INSERT WITH CHECK (
  current_setting('gregale.team_order_write',true)='on'
  AND EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_order_outbox.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role IN ('owner','admin'))
);
CREATE POLICY scoped_attempt_creates ON api.team_order_delivery_attempts FOR INSERT WITH CHECK (
  tenant_id=current_setting('gregale.outbox_write',true)
);
CREATE FUNCTION api.enqueue_team_order_webhook() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE event_key uuid:=gen_random_uuid();
BEGIN
  IF NEW.action IN ('submit','fulfil') THEN
    INSERT INTO api.team_order_outbox(event_id,tenant_id,order_id,version,payload)
    VALUES(event_key,NEW.tenant_id,NEW.order_id,NEW.version,jsonb_build_object(
      'id',event_key,'type',CASE WHEN NEW.action='submit' THEN 'order.submitted' ELSE 'order.fulfilled' END,
      'time',NEW.occurred_at,'team',NEW.tenant_id,'data',jsonb_build_object('order_id',NEW.order_id,'version',NEW.version,'status',NEW.after_status,'total_cents',NEW.total_cents,'actor',NEW.actor)));
  END IF;
  RETURN NEW;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.enqueue_team_order_webhook() FROM PUBLIC;
CREATE TRIGGER enqueue_team_order_webhook AFTER INSERT ON api.team_order_events FOR EACH ROW EXECUTE FUNCTION api.enqueue_team_order_webhook();

CREATE FUNCTION api.claim_team_order_webhook(team_id text,claim_token uuid) RETURNS SETOF api.team_order_outbox
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  old_scope text:=current_setting('gregale.outbox_write',true);
  result api.team_order_outbox;
BEGIN
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='outbox_read_committed_required'; END IF;
  IF team_id IS NULL OR team_id='' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  IF current_setting('request.jwt.claims',true)::jsonb->'outbox_worker' IS DISTINCT FROM 'true'::jsonb OR current_setting('request.jwt.claims',true)::jsonb->>'outbox_team' IS DISTINCT FROM team_id THEN RAISE SQLSTATE '42501' USING MESSAGE='outbox_worker_required'; END IF;
  PERFORM set_config('gregale.outbox_write',team_id,true);
  IF claim_token IS NULL THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:outbox-claim:' || team_id || ':' || claim_token::text,0));
  -- Reusing a live claim token reads the original lease, never a second event.
  SELECT q.* INTO result FROM api.team_order_outbox q WHERE q.tenant_id=team_id AND q.lease_token=claim_token AND q.lease_until>clock_timestamp();
  IF result.event_id IS NULL THEN
    WITH expired AS (
      UPDATE api.team_order_outbox q SET state=CASE WHEN attempts>=5 THEN 'dead'::api.delivery_state ELSE 'pending'::api.delivery_state END,lease_token=NULL,lease_until=NULL
      WHERE q.tenant_id=team_id AND q.state='leased' AND q.lease_until<=clock_timestamp() RETURNING q.*
    ) UPDATE api.team_order_delivery_attempts a SET outcome='expired',finished_at=clock_timestamp()
      FROM expired e WHERE a.event_id=e.event_id AND a.attempt=e.total_attempts AND a.outcome='leased';
    WITH candidate AS (
      SELECT q.event_id FROM api.team_order_outbox q WHERE q.tenant_id=team_id AND q.state='pending' AND q.available_at<=clock_timestamp() AND q.attempts<5
      ORDER BY q.available_at,q.event_id FOR UPDATE SKIP LOCKED LIMIT 1
    ) UPDATE api.team_order_outbox q SET state='leased',attempts=q.attempts+1,total_attempts=q.total_attempts+1,lease_token=claim_token,lease_until=clock_timestamp()+interval '30 seconds'
      FROM candidate c WHERE q.event_id=c.event_id RETURNING q.* INTO result;
    IF result.event_id IS NOT NULL THEN INSERT INTO api.team_order_delivery_attempts(event_id,tenant_id,attempt) VALUES(result.event_id,team_id,result.total_attempts); END IF;
  END IF;
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true);
  IF result.event_id IS NOT NULL THEN RETURN NEXT result; END IF;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.claim_team_order_webhook(text,uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.claim_team_order_webhook(text,uuid) IS '@gregale:rpc';

CREATE FUNCTION api.finish_team_order_webhook(team_id text,delivery_id uuid,claim_token uuid,succeeded boolean,response_status integer) RETURNS SETOF api.team_order_outbox
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  old_scope text:=current_setting('gregale.outbox_write',true);
  result api.team_order_outbox;
BEGIN
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='outbox_read_committed_required'; END IF;
  IF team_id IS NULL OR team_id='' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  IF current_setting('request.jwt.claims',true)::jsonb->'outbox_worker' IS DISTINCT FROM 'true'::jsonb OR current_setting('request.jwt.claims',true)::jsonb->>'outbox_team' IS DISTINCT FROM team_id THEN RAISE SQLSTATE '42501' USING MESSAGE='outbox_worker_required'; END IF;
  PERFORM set_config('gregale.outbox_write',team_id,true);
  IF claim_token IS NULL OR delivery_id IS NULL OR succeeded IS NULL OR response_status IS NOT NULL AND response_status NOT BETWEEN 100 AND 599
    OR succeeded AND (response_status IS NULL OR response_status NOT BETWEEN 200 AND 299) THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  UPDATE api.team_order_outbox q SET state=CASE WHEN succeeded THEN 'delivered'::api.delivery_state WHEN q.attempts>=5 THEN 'dead'::api.delivery_state ELSE 'pending'::api.delivery_state END,
    delivered_at=CASE WHEN succeeded THEN clock_timestamp() ELSE NULL END,lease_token=NULL,lease_until=NULL,
    available_at=clock_timestamp()+make_interval(secs=>LEAST(300,power(2,q.attempts)::integer))
    WHERE q.tenant_id=team_id AND q.event_id=delivery_id AND q.state='leased' AND q.lease_token=claim_token AND q.lease_until>clock_timestamp() RETURNING q.* INTO result;
  IF result.event_id IS NOT NULL THEN UPDATE api.team_order_delivery_attempts a SET outcome=CASE WHEN succeeded THEN 'delivered' ELSE 'failed' END,finished_at=clock_timestamp(),http_status=response_status
    WHERE a.event_id=delivery_id AND a.attempt=result.total_attempts AND a.outcome='leased'; END IF;
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true);
  IF result.event_id IS NOT NULL THEN RETURN NEXT result; END IF;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.finish_team_order_webhook(text,uuid,uuid,boolean,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.finish_team_order_webhook(text,uuid,uuid,boolean,integer) IS '@gregale:rpc';

CREATE FUNCTION api.replay_team_order_webhook(team_id text,delivery_id uuid,expected_generation integer) RETURNS SETOF api.team_order_outbox
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  old_scope text:=current_setting('gregale.outbox_write',true);
  result api.team_order_outbox;
BEGIN
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='outbox_read_committed_required'; END IF;
  IF team_id IS NULL OR team_id='' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner') THEN RAISE SQLSTATE '42501' USING MESSAGE='team_owner_required'; END IF;
  PERFORM set_config('gregale.outbox_write',team_id,true);
  IF delivery_id IS NULL OR expected_generation IS NULL OR expected_generation<0 THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  UPDATE api.team_order_outbox q SET state='pending',attempts=0,generation=q.generation+1,available_at=clock_timestamp(),lease_token=NULL,lease_until=NULL
    WHERE q.tenant_id=team_id AND q.event_id=delivery_id AND q.generation=expected_generation AND (q.state='dead' OR q.state='leased' AND q.attempts>=5 AND q.lease_until<=clock_timestamp()) RETURNING q.* INTO result;
  IF result.event_id IS NOT NULL THEN UPDATE api.team_order_delivery_attempts a SET outcome='expired',finished_at=clock_timestamp()
    WHERE a.event_id=delivery_id AND a.attempt=result.total_attempts AND a.outcome='leased'; END IF;
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true);
  IF result.event_id IS NOT NULL THEN RETURN NEXT result; END IF;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.replay_team_order_webhook(text,uuid,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.replay_team_order_webhook(text,uuid,integer) IS '@gregale:rpc';
