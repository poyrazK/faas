CREATE TABLE api.team_webhook_subscriptions (
  subscription_id uuid PRIMARY KEY,
  tenant_id text NOT NULL,
  name text NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
  event_types text[] NOT NULL CHECK(cardinality(event_types) BETWEEN 1 AND 2 AND event_types <@ ARRAY['order.submitted','order.fulfilled']::text[]),
  config_ref text NOT NULL CHECK(config_ref ~ '^[a-zA-Z0-9_-]{1,80}$'),
  enabled boolean NOT NULL,
  paused boolean NOT NULL,
  version integer NOT NULL DEFAULT 1 CHECK(version>0),
  revision uuid NOT NULL DEFAULT gen_random_uuid(),
  changed_by text NOT NULL,
  changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(tenant_id,name)
);
CREATE TABLE api.team_webhook_subscription_versions (
  subscription_id uuid NOT NULL REFERENCES api.team_webhook_subscriptions(subscription_id),
  version integer NOT NULL CHECK(version>0),
  tenant_id text NOT NULL,
  config_ref text NOT NULL,
  event_types text[] NOT NULL,
  changed_by text NOT NULL,
  changed_at timestamptz NOT NULL,
  PRIMARY KEY(subscription_id,version)
);
CREATE TABLE api.team_webhook_subscription_operations (
  request_id uuid NOT NULL,
  tenant_id text NOT NULL,
  actor text NOT NULL,
  subscription_id uuid NOT NULL,
  action text NOT NULL CHECK(action IN ('create','update')),
  payload jsonb NOT NULL,
  snapshot jsonb NOT NULL,
  occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY(tenant_id,actor,request_id)
);
ALTER TABLE api.team_webhook_subscriptions ENABLE ROW LEVEL SECURITY;
CREATE POLICY subscription_owner_reads ON api.team_webhook_subscriptions FOR SELECT USING (
  EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_webhook_subscriptions.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner')
);
CREATE POLICY scoped_subscription_creates ON api.team_webhook_subscriptions FOR INSERT WITH CHECK(tenant_id=current_setting('gregale.subscription_write',true));
ALTER TABLE api.team_webhook_subscription_versions ENABLE ROW LEVEL SECURITY;
CREATE POLICY subscription_owner_reads ON api.team_webhook_subscription_versions FOR SELECT USING (
  EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_webhook_subscription_versions.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner')
);
CREATE POLICY scoped_subscription_creates ON api.team_webhook_subscription_versions FOR INSERT WITH CHECK(tenant_id=current_setting('gregale.subscription_write',true));
ALTER TABLE api.team_webhook_subscription_operations ENABLE ROW LEVEL SECURITY;
CREATE POLICY subscription_owner_reads ON api.team_webhook_subscription_operations FOR SELECT USING (
  EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_webhook_subscription_operations.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner')
);
CREATE POLICY scoped_subscription_creates ON api.team_webhook_subscription_operations FOR INSERT WITH CHECK(tenant_id=current_setting('gregale.subscription_write',true));
CREATE POLICY scoped_subscription_updates ON api.team_webhook_subscriptions FOR UPDATE USING(tenant_id=current_setting('gregale.subscription_write',true)) WITH CHECK(tenant_id=current_setting('gregale.subscription_write',true));
CREATE POLICY subscription_worker_reads ON api.team_webhook_subscriptions FOR SELECT USING(
  current_setting('request.jwt.claims',true)::jsonb->'outbox_worker'='true'::jsonb AND current_setting('request.jwt.claims',true)::jsonb->>'outbox_team'=tenant_id
  AND current_setting('request.jwt.claims',true)::jsonb->>'outbox_subscription'=subscription_id::text
);
CREATE POLICY subscription_receipt_reads ON api.team_webhook_subscription_operations FOR SELECT USING(tenant_id=current_setting('gregale.subscription_receipt_read',true) AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub');
CREATE FUNCTION api.get_webhook_subscription_receipt(team_id text,operation_key uuid) RETURNS SETOF api.team_webhook_subscription_operations
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE old_scope text:=current_setting('gregale.subscription_receipt_read',true);
BEGIN
  PERFORM set_config('gregale.subscription_receipt_read',COALESCE(team_id,''),true);
  RETURN QUERY SELECT o.* FROM api.team_webhook_subscription_operations o WHERE o.tenant_id=team_id AND o.actor=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND o.request_id=operation_key;
  PERFORM set_config('gregale.subscription_receipt_read',COALESCE(old_scope,''),true);
EXCEPTION WHEN OTHERS THEN PERFORM set_config('gregale.subscription_receipt_read',COALESCE(old_scope,''),true);RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.get_webhook_subscription_receipt(text,uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.get_webhook_subscription_receipt(text,uuid) IS '@gregale:rpc';
CREATE FUNCTION api.save_webhook_subscription_once(team_id text,request_id uuid,subscription_id uuid,expected_revision uuid,subscription_name text,event_filters text[],config_reference text,is_enabled boolean,is_paused boolean)
RETURNS SETOF api.team_webhook_subscription_operations LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text:=current_setting('request.jwt.claims',true)::jsonb->>'sub';
  old_scope text:=current_setting('gregale.subscription_write',true);
  current_row api.team_webhook_subscriptions;
  receipt api.team_webhook_subscription_operations;
  intended jsonb;
  filters text[];
BEGIN
  IF team_id IS NULL OR team_id='' OR request_id IS NULL OR subscription_id IS NULL OR subscription_name IS NULL OR length(subscription_name) NOT BETWEEN 1 AND 80 OR config_reference IS NULL OR config_reference !~ '^[a-zA-Z0-9_-]{1,80}$' OR is_enabled IS NULL OR is_paused IS NULL OR event_filters IS NULL OR cardinality(event_filters) NOT BETWEEN 1 AND 2 OR array_position(event_filters,NULL) IS NOT NULL OR NOT event_filters <@ ARRAY['order.submitted','order.fulfilled']::text[] THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_subscription'; END IF;
  SELECT array_agg(DISTINCT e ORDER BY e) INTO filters FROM unnest(event_filters) e;
  IF cardinality(filters)<>cardinality(event_filters) THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_subscription'; END IF;
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='outbox_read_committed_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:'||team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject AND m.role='owner') THEN RAISE SQLSTATE '42501' USING MESSAGE='team_owner_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:delivery-control:'||team_id,0));
  PERFORM set_config('gregale.subscription_write',team_id,true);
  intended:=jsonb_build_object('id',subscription_id,'revision',expected_revision,'name',subscription_name,'filters',filters,'config',config_reference,'enabled',is_enabled,'paused',is_paused);
  SELECT o.* INTO receipt FROM api.team_webhook_subscription_operations o WHERE o.tenant_id=team_id AND o.actor=caller_subject AND o.request_id=save_webhook_subscription_once.request_id;
  IF FOUND THEN
    IF receipt.payload<>intended THEN RAISE SQLSTATE 'PT409' USING MESSAGE='subscription_payload_conflict'; END IF;
  ELSE
    SELECT s.* INTO current_row FROM api.team_webhook_subscriptions s WHERE s.subscription_id=save_webhook_subscription_once.subscription_id AND s.tenant_id=team_id;
    IF current_row.revision IS DISTINCT FROM expected_revision THEN RAISE SQLSTATE 'PT409' USING MESSAGE='subscription_revision_conflict'; END IF;
    IF current_row.subscription_id IS NULL THEN
      INSERT INTO api.team_webhook_subscriptions(subscription_id,tenant_id,name,event_types,config_ref,enabled,paused,changed_by)
      VALUES(subscription_id,team_id,subscription_name,filters,config_reference,is_enabled,is_paused,caller_subject) RETURNING * INTO current_row;
    ELSE
      UPDATE api.team_webhook_subscriptions s SET name=subscription_name,event_types=filters,config_ref=config_reference,enabled=is_enabled,paused=is_paused,version=s.version+1,revision=gen_random_uuid(),changed_by=caller_subject,changed_at=clock_timestamp()
      WHERE s.subscription_id=save_webhook_subscription_once.subscription_id AND s.tenant_id=team_id RETURNING s.* INTO current_row;
    END IF;
    INSERT INTO api.team_webhook_subscription_versions(subscription_id,version,tenant_id,config_ref,event_types,changed_by,changed_at) VALUES(current_row.subscription_id,current_row.version,team_id,current_row.config_ref,current_row.event_types,caller_subject,current_row.changed_at);
    INSERT INTO api.team_webhook_subscription_operations(request_id,tenant_id,actor,subscription_id,action,payload,snapshot) VALUES(request_id,team_id,caller_subject,subscription_id,CASE WHEN expected_revision IS NULL THEN 'create' ELSE 'update' END,intended,to_jsonb(current_row)) RETURNING * INTO receipt;
  END IF;
  PERFORM set_config('gregale.subscription_write',COALESCE(old_scope,''),true);RETURN NEXT receipt;
EXCEPTION WHEN OTHERS THEN PERFORM set_config('gregale.subscription_write',COALESCE(old_scope,''),true);RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.save_webhook_subscription_once(text,uuid,uuid,uuid,text,text[],text,boolean,boolean) FROM PUBLIC;
COMMENT ON FUNCTION api.save_webhook_subscription_once(text,uuid,uuid,uuid,text,text[],text,boolean,boolean) IS '@gregale:rpc';
CREATE TABLE api.team_subscription_outbox (
  event_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id text NOT NULL,
  subscription_id uuid NOT NULL REFERENCES api.team_webhook_subscriptions(subscription_id),
  subscription_version integer NOT NULL,
  config_ref text NOT NULL,
  enqueued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
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
  UNIQUE(tenant_id,subscription_id,order_id,version),
  UNIQUE(tenant_id,subscription_id,lease_token),
  CHECK((state='leased')=(lease_token IS NOT NULL AND lease_until IS NOT NULL)),
  CHECK((lease_token IS NULL)=(lease_until IS NULL)),
  CHECK((state='delivered')=(delivered_at IS NOT NULL))
);
CREATE INDEX team_subscription_outbox_pending ON api.team_subscription_outbox(tenant_id,available_at,event_id) WHERE state IN ('pending','leased');
CREATE TABLE api.team_subscription_attempts (
  event_id uuid NOT NULL REFERENCES api.team_subscription_outbox(event_id),
  tenant_id text NOT NULL,
  attempt integer NOT NULL CHECK(attempt>0),
  started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  finished_at timestamptz,
  outcome text NOT NULL DEFAULT 'leased' CHECK(outcome IN ('leased','delivered','failed','expired')),
  http_status integer CHECK(http_status BETWEEN 100 AND 599),
  PRIMARY KEY(event_id,attempt),
  CHECK((outcome='leased')=(finished_at IS NULL))
);
ALTER TABLE api.team_subscription_outbox ENABLE ROW LEVEL SECURITY;
CREATE POLICY owner_subscription_worker_reads ON api.team_subscription_outbox FOR SELECT USING (
  EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_subscription_outbox.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner') OR (current_setting('request.jwt.claims',true)::jsonb->'outbox_worker'='true'::jsonb AND current_setting('request.jwt.claims',true)::jsonb->>'outbox_team'=tenant_id AND current_setting('request.jwt.claims',true)::jsonb->>'outbox_subscription'=subscription_id::text)
);
CREATE POLICY scoped_subscription_queue_updates ON api.team_subscription_outbox FOR UPDATE USING(tenant_id=current_setting('gregale.outbox_write',true)) WITH CHECK(tenant_id=current_setting('gregale.outbox_write',true));
ALTER TABLE api.team_subscription_attempts ADD COLUMN subscription_id uuid NOT NULL;
ALTER TABLE api.team_subscription_attempts ENABLE ROW LEVEL SECURITY;
CREATE POLICY owner_subscription_worker_reads ON api.team_subscription_attempts FOR SELECT USING (
  EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_subscription_attempts.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner') OR (current_setting('request.jwt.claims',true)::jsonb->'outbox_worker'='true'::jsonb AND current_setting('request.jwt.claims',true)::jsonb->>'outbox_team'=tenant_id AND current_setting('request.jwt.claims',true)::jsonb->>'outbox_subscription'=subscription_id::text)
);
CREATE POLICY scoped_subscription_queue_updates ON api.team_subscription_attempts FOR UPDATE USING(tenant_id=current_setting('gregale.outbox_write',true)) WITH CHECK(tenant_id=current_setting('gregale.outbox_write',true));
CREATE POLICY subscription_queue_creates ON api.team_subscription_outbox FOR INSERT WITH CHECK(current_setting('gregale.team_order_write',true)='on' AND EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_subscription_outbox.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role IN ('owner','admin')));
CREATE POLICY subscription_attempt_creates ON api.team_subscription_attempts FOR INSERT WITH CHECK(tenant_id=current_setting('gregale.outbox_write',true));
-- Routing is frozen at enqueue. Pausing does not prevent enqueue; disabling does.
CREATE OR REPLACE FUNCTION api.enqueue_team_order_webhook() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE event_key uuid; destination api.team_webhook_subscriptions; event_type text;
BEGIN
  IF NEW.action IN ('submit','fulfil') THEN
    event_type:=CASE WHEN NEW.action='submit' THEN 'order.submitted' ELSE 'order.fulfilled' END;
    -- Routing reads must work for admins too; a scoped SELECT policy is enabled
    -- only during this reviewed order write.
    IF EXISTS(SELECT 1 FROM api.team_webhook_subscriptions s WHERE s.tenant_id=NEW.tenant_id) THEN
      FOR destination IN SELECT s.* FROM api.team_webhook_subscriptions s WHERE s.tenant_id=NEW.tenant_id AND s.enabled AND event_type=ANY(s.event_types) LOOP
        event_key:=gen_random_uuid();
        INSERT INTO api.team_subscription_outbox(event_id,tenant_id,subscription_id,subscription_version,config_ref,order_id,version,payload)
        VALUES(event_key,NEW.tenant_id,destination.subscription_id,destination.version,destination.config_ref,NEW.order_id,NEW.version,jsonb_build_object('id',event_key,'type',event_type,'time',NEW.occurred_at,'team',NEW.tenant_id,'data',jsonb_build_object('order_id',NEW.order_id,'version',NEW.version,'status',NEW.after_status,'total_cents',NEW.total_cents,'actor',NEW.actor)));
      END LOOP;
    ELSE
      event_key:=gen_random_uuid();
      INSERT INTO api.team_order_outbox(event_id,tenant_id,order_id,version,payload) VALUES(event_key,NEW.tenant_id,NEW.order_id,NEW.version,jsonb_build_object('id',event_key,'type',event_type,'time',NEW.occurred_at,'team',NEW.tenant_id,'data',jsonb_build_object('order_id',NEW.order_id,'version',NEW.version,'status',NEW.after_status,'total_cents',NEW.total_cents,'actor',NEW.actor)));
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
CREATE POLICY reviewed_subscription_routing ON api.team_webhook_subscriptions FOR SELECT USING(current_setting('gregale.team_order_write',true)='on' AND EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_webhook_subscriptions.tenant_id AND m.role IN ('owner','admin')));

CREATE FUNCTION api.claim_subscription_webhook(team_id text,subscription_id uuid,claim_token uuid) RETURNS SETOF api.team_subscription_outbox
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  old_scope text:=current_setting('gregale.outbox_write',true);
  result api.team_subscription_outbox;
BEGIN
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='outbox_read_committed_required'; END IF;
  IF team_id IS NULL OR team_id='' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  IF current_setting('request.jwt.claims',true)::jsonb->'outbox_worker' IS DISTINCT FROM 'true'::jsonb OR current_setting('request.jwt.claims',true)::jsonb->>'outbox_team' IS DISTINCT FROM team_id THEN RAISE SQLSTATE '42501' USING MESSAGE='outbox_worker_required'; END IF;
  PERFORM pg_advisory_xact_lock_shared(hashtextextended('gregale:delivery-control:' || team_id,0));
  IF subscription_id IS NULL THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_subscription'; END IF;
  IF current_setting('request.jwt.claims',true)::jsonb->>'outbox_subscription' IS DISTINCT FROM subscription_id::text THEN RAISE SQLSTATE '42501' USING MESSAGE='subscription_worker_required'; END IF;
  PERFORM set_config('gregale.outbox_write',team_id,true);
  IF claim_token IS NULL THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:outbox-claim:' || team_id || ':' || claim_token::text,0));
  -- Reusing a live claim token reads the original lease, never a second event.
  SELECT q.* INTO result FROM api.team_subscription_outbox q WHERE q.tenant_id=team_id AND q.subscription_id=claim_subscription_webhook.subscription_id AND q.lease_token=claim_token AND q.lease_until>clock_timestamp();
  IF result.event_id IS NULL AND NOT EXISTS(SELECT 1 FROM api.team_webhook_subscriptions s WHERE s.subscription_id=claim_subscription_webhook.subscription_id AND s.tenant_id=team_id AND s.paused) AND NOT EXISTS(SELECT 1 FROM api.team_delivery_controls c WHERE c.tenant_id=team_id AND c.paused) THEN
    WITH expired AS (
      UPDATE api.team_subscription_outbox q SET state=CASE WHEN attempts>=5 THEN 'dead'::api.delivery_state ELSE 'pending'::api.delivery_state END,lease_token=NULL,lease_until=NULL
      WHERE q.tenant_id=team_id AND q.subscription_id=claim_subscription_webhook.subscription_id AND q.state='leased' AND q.lease_until<=clock_timestamp() RETURNING q.*
    ) UPDATE api.team_subscription_attempts a SET outcome='expired',finished_at=clock_timestamp()
      FROM expired e WHERE a.event_id=e.event_id AND a.attempt=e.total_attempts AND a.outcome='leased';
    WITH candidate AS (
      SELECT q.event_id FROM api.team_subscription_outbox q WHERE q.tenant_id=team_id AND q.subscription_id=claim_subscription_webhook.subscription_id AND q.state='pending' AND q.available_at<=clock_timestamp() AND q.attempts<5
      ORDER BY q.available_at,q.event_id FOR UPDATE SKIP LOCKED LIMIT 1
    ) UPDATE api.team_subscription_outbox q SET state='leased',attempts=q.attempts+1,total_attempts=q.total_attempts+1,lease_token=claim_token,lease_until=clock_timestamp()+interval '30 seconds'
      FROM candidate c WHERE q.event_id=c.event_id RETURNING q.* INTO result;
    IF result.event_id IS NOT NULL THEN INSERT INTO api.team_subscription_attempts(event_id,tenant_id,subscription_id,attempt) VALUES(result.event_id,team_id,subscription_id,result.total_attempts); END IF;
  END IF;
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true);
  IF result.event_id IS NOT NULL THEN RETURN NEXT result; END IF;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.claim_subscription_webhook(text,uuid,uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.claim_subscription_webhook(text,uuid,uuid) IS '@gregale:rpc';
CREATE FUNCTION api.finish_subscription_webhook(team_id text,subscription_id uuid,delivery_id uuid,claim_token uuid,succeeded boolean,response_status integer) RETURNS SETOF api.team_subscription_outbox
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  old_scope text:=current_setting('gregale.outbox_write',true);
  result api.team_subscription_outbox;
BEGIN
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='outbox_read_committed_required'; END IF;
  IF team_id IS NULL OR team_id='' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  IF current_setting('request.jwt.claims',true)::jsonb->'outbox_worker' IS DISTINCT FROM 'true'::jsonb OR current_setting('request.jwt.claims',true)::jsonb->>'outbox_team' IS DISTINCT FROM team_id THEN RAISE SQLSTATE '42501' USING MESSAGE='outbox_worker_required'; END IF;
  IF subscription_id IS NULL THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_subscription'; END IF;
  IF current_setting('request.jwt.claims',true)::jsonb->>'outbox_subscription' IS DISTINCT FROM subscription_id::text THEN RAISE SQLSTATE '42501' USING MESSAGE='subscription_worker_required'; END IF;
  PERFORM set_config('gregale.outbox_write',team_id,true);
  IF claim_token IS NULL OR delivery_id IS NULL OR succeeded IS NULL OR response_status IS NOT NULL AND response_status NOT BETWEEN 100 AND 599
    OR succeeded AND (response_status IS NULL OR response_status NOT BETWEEN 200 AND 299) THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  UPDATE api.team_subscription_outbox q SET state=CASE WHEN succeeded THEN 'delivered'::api.delivery_state WHEN q.attempts>=5 THEN 'dead'::api.delivery_state ELSE 'pending'::api.delivery_state END,
    delivered_at=CASE WHEN succeeded THEN clock_timestamp() ELSE NULL END,lease_token=NULL,lease_until=NULL,
    available_at=clock_timestamp()+make_interval(secs=>LEAST(300,power(2,q.attempts)::integer))
    WHERE q.tenant_id=team_id AND q.subscription_id=finish_subscription_webhook.subscription_id AND q.event_id=delivery_id AND q.state='leased' AND q.lease_token=claim_token AND q.lease_until>clock_timestamp() RETURNING q.* INTO result;
  IF result.event_id IS NOT NULL THEN UPDATE api.team_subscription_attempts a SET outcome=CASE WHEN succeeded THEN 'delivered' ELSE 'failed' END,finished_at=clock_timestamp(),http_status=response_status
    WHERE a.event_id=delivery_id AND a.attempt=result.total_attempts AND a.outcome='leased'; END IF;
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true);
  IF result.event_id IS NOT NULL THEN RETURN NEXT result; END IF;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.finish_subscription_webhook(text,uuid,uuid,uuid,boolean,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.finish_subscription_webhook(text,uuid,uuid,uuid,boolean,integer) IS '@gregale:rpc';
CREATE FUNCTION api.replay_subscription_webhook(team_id text,subscription_id uuid,delivery_id uuid,expected_generation integer) RETURNS SETOF api.team_subscription_outbox
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  old_scope text:=current_setting('gregale.outbox_write',true);
  result api.team_subscription_outbox;
BEGIN
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='outbox_read_committed_required'; END IF;
  IF team_id IS NULL OR team_id='' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner') THEN RAISE SQLSTATE '42501' USING MESSAGE='team_owner_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:delivery-control:' || team_id,0));
  IF subscription_id IS NULL THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_subscription'; END IF;
  PERFORM set_config('gregale.outbox_write',team_id,true);
  IF delivery_id IS NULL OR expected_generation IS NULL OR expected_generation<0 THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  UPDATE api.team_subscription_outbox q SET state='pending',attempts=0,generation=q.generation+1,available_at=clock_timestamp(),lease_token=NULL,lease_until=NULL
    WHERE q.tenant_id=team_id AND q.subscription_id=replay_subscription_webhook.subscription_id AND q.event_id=delivery_id AND q.generation=expected_generation AND (q.state='dead' OR q.state='leased' AND q.attempts>=5 AND q.lease_until<=clock_timestamp()) RETURNING q.* INTO result;
  IF result.event_id IS NOT NULL THEN UPDATE api.team_subscription_attempts a SET outcome='expired',finished_at=clock_timestamp()
    WHERE a.event_id=delivery_id AND a.attempt=result.total_attempts AND a.outcome='leased'; END IF;
  IF result.event_id IS NOT NULL THEN
    INSERT INTO api.team_delivery_operations(operation_id,tenant_id,actor,action,payload,result)
    VALUES(gen_random_uuid(),team_id,current_setting('request.jwt.claims',true)::jsonb->>'sub','replay',jsonb_build_object('subscription_id',subscription_id,'event_id',delivery_id,'generation',expected_generation),jsonb_build_object('subscription_id',subscription_id,'event_id',delivery_id,'generation',result.generation));
  END IF;
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true);
  IF result.event_id IS NOT NULL THEN RETURN NEXT result; END IF;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.replay_subscription_webhook(text,uuid,uuid,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.replay_subscription_webhook(text,uuid,uuid,integer) IS '@gregale:rpc';
CREATE FUNCTION api.get_subscription_health(team_id text,subscription_id uuid) RETURNS jsonb
LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE result jsonb; metadata api.team_webhook_subscriptions;
BEGIN
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner') THEN RAISE SQLSTATE '42501' USING MESSAGE='team_owner_required'; END IF;
  SELECT s.* INTO metadata FROM api.team_webhook_subscriptions s WHERE s.tenant_id=team_id AND s.subscription_id=get_subscription_health.subscription_id;
  IF NOT FOUND THEN RAISE SQLSTATE 'PT409' USING MESSAGE='subscription_unavailable'; END IF;
  SELECT jsonb_build_object('subscription_id',metadata.subscription_id,'paused',metadata.paused,'enabled',metadata.enabled,'version',metadata.version,
    'pending',count(*) FILTER(WHERE effective='pending'),'leased',count(*) FILTER(WHERE effective='leased'),'delivered',count(*) FILTER(WHERE effective='delivered'),'dead',count(*) FILTER(WHERE effective='dead'),
    'oldest_pending_seconds',CASE WHEN min(enqueued_at) FILTER(WHERE effective='pending') IS NULL THEN NULL ELSE GREATEST(0,floor(extract(epoch FROM statement_timestamp()-min(enqueued_at) FILTER(WHERE effective='pending')))) END)
  INTO result FROM (SELECT q.enqueued_at,CASE WHEN q.state='leased' AND q.lease_until<=statement_timestamp() THEN CASE WHEN q.attempts>=5 THEN 'dead' ELSE 'pending' END ELSE q.state::text END AS effective FROM api.team_subscription_outbox q WHERE q.tenant_id=team_id AND q.subscription_id=get_subscription_health.subscription_id) rows;
  RETURN result;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.get_subscription_health(text,uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.get_subscription_health(text,uuid) IS '@gregale:rpc';
