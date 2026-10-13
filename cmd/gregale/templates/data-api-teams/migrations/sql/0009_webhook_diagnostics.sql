-- Customer-owned diagnostics and explicitly marked sample deliveries.
ALTER TABLE api.team_subscription_outbox ADD COLUMN is_test boolean NOT NULL DEFAULT false;
ALTER TABLE api.team_subscription_attempts ADD COLUMN failure_reason text
  CHECK(failure_reason IN ('configuration_missing','network_error','http_error'));
ALTER TABLE api.team_subscription_attempts ADD CONSTRAINT subscription_failure_outcome
  CHECK(failure_reason IS NULL OR outcome='failed');
CREATE INDEX subscription_delivery_browse ON api.team_subscription_outbox(tenant_id,subscription_id,event_id);
CREATE INDEX subscription_pending_tests ON api.team_subscription_outbox(tenant_id,subscription_id) WHERE is_test AND state IN ('pending','leased');
CREATE INDEX subscription_change_browse ON api.team_webhook_subscription_operations(tenant_id,subscription_id,request_id);
CREATE TABLE api.team_webhook_test_receipts (
  tenant_id text NOT NULL,
  actor text NOT NULL,
  request_id uuid NOT NULL,
  subscription_id uuid NOT NULL REFERENCES api.team_webhook_subscriptions(subscription_id),
  expected_revision uuid NOT NULL,
  event_id uuid NOT NULL REFERENCES api.team_subscription_outbox(event_id),
  subscription_version integer NOT NULL CHECK(subscription_version>0),
  config_ref text NOT NULL,
  occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY(tenant_id,actor,request_id)
);
ALTER TABLE api.team_webhook_test_receipts ENABLE ROW LEVEL SECURITY;
CREATE POLICY webhook_test_owner_reads ON api.team_webhook_test_receipts FOR SELECT USING(
  EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_webhook_test_receipts.tenant_id
    AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner')
);
CREATE POLICY webhook_test_receipt_reads ON api.team_webhook_test_receipts FOR SELECT USING(
  tenant_id=current_setting('gregale.webhook_test_receipt',true) AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub'
);
CREATE POLICY reviewed_webhook_test_receipt_create ON api.team_webhook_test_receipts FOR INSERT
  WITH CHECK(tenant_id=current_setting('gregale.webhook_test_write',true));
CREATE POLICY reviewed_webhook_test_enqueue ON api.team_subscription_outbox FOR INSERT
  WITH CHECK(tenant_id=current_setting('gregale.webhook_test_write',true) AND is_test);

CREATE FUNCTION api.get_webhook_test_receipt(team_id text,operation_key uuid)
RETURNS SETOF api.team_webhook_test_receipts LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE old_scope text:=current_setting('gregale.webhook_test_receipt',true);
BEGIN
  PERFORM set_config('gregale.webhook_test_receipt',COALESCE(team_id,''),true);
  RETURN QUERY SELECT r.* FROM api.team_webhook_test_receipts r WHERE r.tenant_id=team_id
    AND r.actor=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND r.request_id=operation_key;
  PERFORM set_config('gregale.webhook_test_receipt',COALESCE(old_scope,''),true);
EXCEPTION WHEN OTHERS THEN PERFORM set_config('gregale.webhook_test_receipt',COALESCE(old_scope,''),true);RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.get_webhook_test_receipt(text,uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.get_webhook_test_receipt(text,uuid) IS '@gregale:rpc';

CREATE FUNCTION api.queue_webhook_test_once(team_id text,request_id uuid,subscription_id uuid,expected_revision uuid)
RETURNS SETOF api.team_webhook_test_receipts
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  old_scope text:=current_setting('gregale.webhook_test_write',true);
  caller_subject text:=current_setting('request.jwt.claims',true)::jsonb->>'sub';
  receipt api.team_webhook_test_receipts;
  metadata api.team_webhook_subscriptions;
  delivery_id uuid:=gen_random_uuid();
  sample_id uuid:=gen_random_uuid();
BEGIN
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='outbox_read_committed_required'; END IF;
  IF team_id IS NULL OR team_id='' OR request_id IS NULL OR subscription_id IS NULL OR expected_revision IS NULL THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_webhook_test'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:'||team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject AND m.role='owner') THEN
    RAISE SQLSTATE '42501' USING MESSAGE='team_owner_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:delivery-control:'||team_id,0));
  SELECT r.* INTO receipt FROM api.team_webhook_test_receipts r WHERE r.tenant_id=team_id AND r.actor=caller_subject AND r.request_id=queue_webhook_test_once.request_id;
  IF FOUND THEN
    IF receipt.subscription_id<>subscription_id OR receipt.expected_revision<>expected_revision THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE='webhook_test_payload_conflict'; END IF;
    RETURN NEXT receipt; RETURN;
  END IF;
  SELECT s.* INTO metadata FROM api.team_webhook_subscriptions s WHERE s.tenant_id=team_id AND s.subscription_id=queue_webhook_test_once.subscription_id;
  IF metadata.revision IS DISTINCT FROM expected_revision THEN RAISE SQLSTATE 'PT409' USING MESSAGE='subscription_revision_conflict'; END IF;
  IF EXISTS(SELECT 1 FROM api.team_subscription_outbox q WHERE q.tenant_id=team_id AND q.subscription_id=queue_webhook_test_once.subscription_id AND q.is_test AND q.state IN ('pending','leased')) THEN
    RAISE SQLSTATE 'PT409' USING MESSAGE='webhook_test_pending'; END IF;
  PERFORM set_config('gregale.webhook_test_write',team_id,true);
  -- The queue's order correlation fields are synthetic; no order or order event is written.
  INSERT INTO api.team_subscription_outbox(event_id,tenant_id,subscription_id,subscription_version,config_ref,order_id,version,is_test,payload)
  VALUES(delivery_id,team_id,subscription_id,metadata.version,metadata.config_ref,sample_id,2,true,
    jsonb_build_object('id',delivery_id,'type','webhook.test','test',true,'time',clock_timestamp(),'team',team_id,
      'data',jsonb_build_object('message','Gregale webhook test','requested_by',caller_subject)));
  INSERT INTO api.team_webhook_test_receipts(tenant_id,actor,request_id,subscription_id,expected_revision,event_id,subscription_version,config_ref)
  VALUES(team_id,caller_subject,request_id,subscription_id,expected_revision,delivery_id,metadata.version,metadata.config_ref) RETURNING * INTO receipt;
  PERFORM set_config('gregale.webhook_test_write',COALESCE(old_scope,''),true);
  RETURN NEXT receipt;
EXCEPTION WHEN OTHERS THEN PERFORM set_config('gregale.webhook_test_write',COALESCE(old_scope,''),true);RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.queue_webhook_test_once(text,uuid,uuid,uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.queue_webhook_test_once(text,uuid,uuid,uuid) IS '@gregale:rpc';

DROP FUNCTION api.finish_subscription_webhook(text,uuid,uuid,uuid,boolean,integer);
CREATE FUNCTION api.finish_subscription_webhook(team_id text,subscription_id uuid,delivery_id uuid,claim_token uuid,succeeded boolean,response_status integer,failure_reason text DEFAULT NULL) RETURNS SETOF api.team_subscription_outbox
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
  IF failure_reason IS NOT NULL AND (succeeded OR failure_reason NOT IN ('configuration_missing','network_error','http_error') OR (failure_reason='http_error')<>(response_status IS NOT NULL)) THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  UPDATE api.team_subscription_outbox q SET state=CASE WHEN succeeded THEN 'delivered'::api.delivery_state WHEN q.attempts>=5 THEN 'dead'::api.delivery_state ELSE 'pending'::api.delivery_state END,
    delivered_at=CASE WHEN succeeded THEN clock_timestamp() ELSE NULL END,lease_token=NULL,lease_until=NULL,
    available_at=clock_timestamp()+make_interval(secs=>LEAST(300,power(2,q.attempts)::integer))
    WHERE q.tenant_id=team_id AND q.subscription_id=finish_subscription_webhook.subscription_id AND q.event_id=delivery_id AND q.state='leased' AND q.lease_token=claim_token AND q.lease_until>clock_timestamp() RETURNING q.* INTO result;
  IF result.event_id IS NOT NULL THEN UPDATE api.team_subscription_attempts a SET outcome=CASE WHEN succeeded THEN 'delivered' ELSE 'failed' END,finished_at=clock_timestamp(),http_status=response_status, failure_reason=CASE WHEN succeeded THEN NULL ELSE COALESCE(finish_subscription_webhook.failure_reason,CASE WHEN response_status IS NULL THEN 'network_error' ELSE 'http_error' END) END
    WHERE a.event_id=delivery_id AND a.attempt=result.total_attempts AND a.outcome='leased'; END IF;
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true);
  IF result.event_id IS NOT NULL THEN RETURN NEXT result; END IF;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.finish_subscription_webhook(text,uuid,uuid,uuid,boolean,integer,text) FROM PUBLIC;
COMMENT ON FUNCTION api.finish_subscription_webhook(text,uuid,uuid,uuid,boolean,integer,text) IS '@gregale:rpc';

-- Replaying a failed sample must obey the same outstanding-sample bound.
CREATE OR REPLACE FUNCTION api.replay_subscription_webhook(team_id text,subscription_id uuid,delivery_id uuid,expected_generation integer) RETURNS SETOF api.team_subscription_outbox
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
  IF EXISTS(SELECT 1 FROM api.team_subscription_outbox q WHERE q.tenant_id=team_id AND q.subscription_id=replay_subscription_webhook.subscription_id AND q.event_id=delivery_id AND q.is_test)
    AND EXISTS(SELECT 1 FROM api.team_subscription_outbox q WHERE q.tenant_id=team_id AND q.subscription_id=replay_subscription_webhook.subscription_id AND q.event_id<>delivery_id AND q.is_test AND q.state IN ('pending','leased')) THEN
    RAISE SQLSTATE 'PT409' USING MESSAGE='webhook_test_pending';
  END IF;
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
