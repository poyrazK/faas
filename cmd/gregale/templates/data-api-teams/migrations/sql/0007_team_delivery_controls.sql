ALTER TABLE api.team_order_outbox ADD COLUMN enqueued_at timestamptz NOT NULL DEFAULT clock_timestamp();
UPDATE api.team_order_outbox q SET enqueued_at=e.occurred_at FROM api.team_order_events e WHERE e.tenant_id=q.tenant_id AND e.order_id=q.order_id AND e.version=q.version;
CREATE TABLE api.team_delivery_controls (
  tenant_id text PRIMARY KEY,
  paused boolean NOT NULL,
  revision uuid NOT NULL DEFAULT gen_random_uuid(),
  changed_by text NOT NULL,
  changed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
ALTER TABLE api.team_delivery_controls ENABLE ROW LEVEL SECURITY;
CREATE POLICY owner_worker_control_reads ON api.team_delivery_controls FOR SELECT USING (
  EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_delivery_controls.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner')
  OR (current_setting('request.jwt.claims',true)::jsonb->'outbox_worker'='true'::jsonb AND current_setting('request.jwt.claims',true)::jsonb->>'outbox_team'=tenant_id)
);
CREATE POLICY scoped_control_creates ON api.team_delivery_controls FOR INSERT WITH CHECK(tenant_id=current_setting('gregale.delivery_control_write',true));
CREATE POLICY scoped_control_updates ON api.team_delivery_controls FOR UPDATE USING(tenant_id=current_setting('gregale.delivery_control_write',true)) WITH CHECK(tenant_id=current_setting('gregale.delivery_control_write',true));
CREATE TABLE api.team_delivery_operations (
  operation_id uuid NOT NULL,
  tenant_id text NOT NULL,
  actor text NOT NULL,
  action text NOT NULL CHECK(action IN ('pause','resume','batch_replay','replay')),
  occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  payload jsonb NOT NULL,
  result jsonb NOT NULL,
  PRIMARY KEY(tenant_id,actor,operation_id)
);
ALTER TABLE api.team_delivery_operations ENABLE ROW LEVEL SECURITY;
CREATE POLICY owner_operation_reads ON api.team_delivery_operations FOR SELECT USING (
  EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_delivery_operations.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner')
  OR (tenant_id=current_setting('gregale.delivery_receipt_read',true) AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub')
);
CREATE POLICY scoped_operation_creates ON api.team_delivery_operations FOR INSERT WITH CHECK (
  tenant_id=current_setting('gregale.outbox_write',true) AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub'
);

CREATE OR REPLACE FUNCTION api.claim_team_order_webhook(team_id text,claim_token uuid) RETURNS SETOF api.team_order_outbox
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  old_scope text:=current_setting('gregale.outbox_write',true);
  result api.team_order_outbox;
BEGIN
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='outbox_read_committed_required'; END IF;
  IF team_id IS NULL OR team_id='' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  IF current_setting('request.jwt.claims',true)::jsonb->'outbox_worker' IS DISTINCT FROM 'true'::jsonb OR current_setting('request.jwt.claims',true)::jsonb->>'outbox_team' IS DISTINCT FROM team_id THEN RAISE SQLSTATE '42501' USING MESSAGE='outbox_worker_required'; END IF;
  PERFORM pg_advisory_xact_lock_shared(hashtextextended('gregale:delivery-control:' || team_id,0));
  PERFORM set_config('gregale.outbox_write',team_id,true);
  IF claim_token IS NULL THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:outbox-claim:' || team_id || ':' || claim_token::text,0));
  -- Reusing a live claim token reads the original lease, never a second event.
  SELECT q.* INTO result FROM api.team_order_outbox q WHERE q.tenant_id=team_id AND q.lease_token=claim_token AND q.lease_until>clock_timestamp();
  IF result.event_id IS NULL AND NOT EXISTS(SELECT 1 FROM api.team_delivery_controls c WHERE c.tenant_id=team_id AND c.paused) THEN
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


CREATE OR REPLACE FUNCTION api.replay_team_order_webhook(team_id text,delivery_id uuid,expected_generation integer) RETURNS SETOF api.team_order_outbox
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  old_scope text:=current_setting('gregale.outbox_write',true);
  result api.team_order_outbox;
BEGIN
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='outbox_read_committed_required'; END IF;
  IF team_id IS NULL OR team_id='' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner') THEN RAISE SQLSTATE '42501' USING MESSAGE='team_owner_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:delivery-control:' || team_id,0));
  PERFORM set_config('gregale.outbox_write',team_id,true);
  IF delivery_id IS NULL OR expected_generation IS NULL OR expected_generation<0 THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_outbox_request'; END IF;
  UPDATE api.team_order_outbox q SET state='pending',attempts=0,generation=q.generation+1,available_at=clock_timestamp(),lease_token=NULL,lease_until=NULL
    WHERE q.tenant_id=team_id AND q.event_id=delivery_id AND q.generation=expected_generation AND (q.state='dead' OR q.state='leased' AND q.attempts>=5 AND q.lease_until<=clock_timestamp()) RETURNING q.* INTO result;
  IF result.event_id IS NOT NULL THEN UPDATE api.team_order_delivery_attempts a SET outcome='expired',finished_at=clock_timestamp()
    WHERE a.event_id=delivery_id AND a.attempt=result.total_attempts AND a.outcome='leased'; END IF;
  IF result.event_id IS NOT NULL THEN
    INSERT INTO api.team_delivery_operations(operation_id,tenant_id,actor,action,payload,result)
    VALUES(gen_random_uuid(),team_id,current_setting('request.jwt.claims',true)::jsonb->>'sub','replay',jsonb_build_object('event_id',delivery_id,'generation',expected_generation),jsonb_build_object('event_id',delivery_id,'generation',result.generation));
  END IF;
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true);
  IF result.event_id IS NOT NULL THEN RETURN NEXT result; END IF;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.outbox_write',COALESCE(old_scope,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.replay_team_order_webhook(text,uuid,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.replay_team_order_webhook(text,uuid,integer) IS '@gregale:rpc';

CREATE FUNCTION api.get_team_delivery_health(team_id text) RETURNS jsonb
LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE health jsonb; control api.team_delivery_controls;
BEGIN
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner') THEN RAISE SQLSTATE '42501' USING MESSAGE='team_owner_required'; END IF;
  SELECT c.* INTO control FROM api.team_delivery_controls c WHERE c.tenant_id=team_id;
  SELECT jsonb_build_object('team',team_id,'paused',COALESCE(control.paused,false),'revision',control.revision,
    'pending',count(*) FILTER(WHERE effective='pending'),'leased',count(*) FILTER(WHERE effective='leased'),
    'delivered',count(*) FILTER(WHERE effective='delivered'),'dead',count(*) FILTER(WHERE effective='dead'),
    'oldest_pending_seconds',CASE WHEN min(enqueued_at) FILTER(WHERE effective='pending') IS NULL THEN NULL ELSE GREATEST(0,floor(extract(epoch FROM statement_timestamp()-min(enqueued_at) FILTER(WHERE effective='pending')))) END)
  INTO health FROM (SELECT q.enqueued_at,CASE WHEN q.state='leased' AND q.lease_until<=statement_timestamp() THEN CASE WHEN q.attempts>=5 THEN 'dead' ELSE 'pending' END ELSE q.state::text END AS effective FROM api.team_order_outbox q WHERE q.tenant_id=team_id) rows;
  RETURN health;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.get_team_delivery_health(text) FROM PUBLIC;
COMMENT ON FUNCTION api.get_team_delivery_health(text) IS '@gregale:rpc';
CREATE FUNCTION api.get_team_delivery_operation(team_id text,request_id uuid) RETURNS SETOF api.team_delivery_operations
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE old_scope text:=current_setting('gregale.delivery_receipt_read',true);
BEGIN
  PERFORM set_config('gregale.delivery_receipt_read',COALESCE(team_id,''),true);
  RETURN QUERY SELECT o.* FROM api.team_delivery_operations o WHERE o.tenant_id=team_id AND o.actor=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND o.operation_id=request_id;
  PERFORM set_config('gregale.delivery_receipt_read',COALESCE(old_scope,''),true);
EXCEPTION WHEN OTHERS THEN PERFORM set_config('gregale.delivery_receipt_read',COALESCE(old_scope,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.get_team_delivery_operation(text,uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.get_team_delivery_operation(text,uuid) IS '@gregale:rpc';
CREATE FUNCTION api.preview_team_delivery_replay(team_id text,page_size integer DEFAULT 20) RETURNS SETOF api.team_order_outbox
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF page_size IS NULL OR page_size NOT BETWEEN 1 AND 20 THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_replay_preview'; END IF;
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner') THEN RAISE SQLSTATE '42501' USING MESSAGE='team_owner_required'; END IF;
  RETURN QUERY SELECT q.* FROM api.team_order_outbox q WHERE q.tenant_id=team_id AND (q.state='dead' OR q.state='leased' AND q.attempts>=5 AND q.lease_until<=statement_timestamp()) ORDER BY q.event_id LIMIT page_size;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.preview_team_delivery_replay(text,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.preview_team_delivery_replay(text,integer) IS '@gregale:rpc';

CREATE FUNCTION api.set_team_delivery_pause_once(team_id text,operation_id uuid,expected_revision uuid,pause_delivery boolean) RETURNS SETOF api.team_delivery_operations
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text:=current_setting('request.jwt.claims',true)::jsonb->>'sub';
  old_write text:=current_setting('gregale.outbox_write',true);
  old_control text:=current_setting('gregale.delivery_control_write',true);
  receipt api.team_delivery_operations;
  control api.team_delivery_controls;
  target api.team_order_outbox;
  item jsonb;
  intended jsonb;
  results jsonb:='[]'::jsonb;
BEGIN
  IF operation_id IS NULL OR team_id IS NULL OR team_id='' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_delivery_operation'; END IF;
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='outbox_read_committed_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject AND m.role='owner') THEN RAISE SQLSTATE '42501' USING MESSAGE='team_owner_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:delivery-control:' || team_id,0));
  PERFORM set_config('gregale.outbox_write',team_id,true);
  PERFORM set_config('gregale.delivery_control_write',team_id,true);
  IF pause_delivery IS NULL THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_delivery_operation'; END IF;
  intended:=jsonb_build_object('revision',expected_revision,'paused',pause_delivery);
  SELECT o.* INTO receipt FROM api.team_delivery_operations o WHERE o.tenant_id=team_id AND o.actor=caller_subject AND o.operation_id=set_team_delivery_pause_once.operation_id;
  IF FOUND THEN
    IF receipt.action<>(CASE WHEN pause_delivery THEN 'pause' ELSE 'resume' END) OR receipt.payload<>intended THEN RAISE SQLSTATE 'PT409' USING MESSAGE='delivery_payload_conflict'; END IF;
  ELSE
    SELECT c.* INTO control FROM api.team_delivery_controls c WHERE c.tenant_id=team_id;
    IF control.revision IS DISTINCT FROM expected_revision THEN RAISE SQLSTATE 'PT409' USING MESSAGE='delivery_revision_conflict'; END IF;
    IF COALESCE(control.paused,false)=pause_delivery THEN RAISE SQLSTATE 'PT409' USING MESSAGE='delivery_control_unchanged'; END IF;
    INSERT INTO api.team_delivery_controls(tenant_id,paused,changed_by) VALUES(team_id,pause_delivery,caller_subject)
      ON CONFLICT(tenant_id) DO UPDATE SET paused=EXCLUDED.paused,revision=gen_random_uuid(),changed_by=caller_subject,changed_at=clock_timestamp() RETURNING * INTO control;
    INSERT INTO api.team_delivery_operations(operation_id,tenant_id,actor,action,payload,result)
      VALUES(operation_id,team_id,caller_subject,CASE WHEN pause_delivery THEN 'pause' ELSE 'resume' END,intended,jsonb_build_object('paused',control.paused,'revision',control.revision)) RETURNING * INTO receipt;
  END IF;
  PERFORM set_config('gregale.outbox_write',COALESCE(old_write,''),true);
  PERFORM set_config('gregale.delivery_control_write',COALESCE(old_control,''),true);
  RETURN NEXT receipt;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.outbox_write',COALESCE(old_write,''),true);
  PERFORM set_config('gregale.delivery_control_write',COALESCE(old_control,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.set_team_delivery_pause_once(text,uuid,uuid,boolean) FROM PUBLIC;
COMMENT ON FUNCTION api.set_team_delivery_pause_once(text,uuid,uuid,boolean) IS '@gregale:rpc';

CREATE FUNCTION api.batch_replay_team_deliveries_once(team_id text,operation_id uuid,reviewed jsonb) RETURNS SETOF api.team_delivery_operations
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text:=current_setting('request.jwt.claims',true)::jsonb->>'sub';
  old_write text:=current_setting('gregale.outbox_write',true);
  old_control text:=current_setting('gregale.delivery_control_write',true);
  receipt api.team_delivery_operations;
  control api.team_delivery_controls;
  target api.team_order_outbox;
  item jsonb;
  intended jsonb;
  results jsonb:='[]'::jsonb;
BEGIN
  IF operation_id IS NULL OR team_id IS NULL OR team_id='' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_delivery_operation'; END IF;
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='outbox_read_committed_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject AND m.role='owner') THEN RAISE SQLSTATE '42501' USING MESSAGE='team_owner_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:delivery-control:' || team_id,0));
  PERFORM set_config('gregale.outbox_write',team_id,true);
  PERFORM set_config('gregale.delivery_control_write',team_id,true);
  IF reviewed IS NULL OR jsonb_typeof(reviewed)<>'array' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_replay_preview'; END IF;
  IF jsonb_array_length(reviewed) NOT BETWEEN 1 AND 20 OR octet_length(reviewed::text)>8192 THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_replay_preview'; END IF;
  FOR item IN SELECT value FROM jsonb_array_elements(reviewed) LOOP
    IF jsonb_typeof(item)<>'object' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_replay_preview'; END IF;
    IF (SELECT count(*) FROM jsonb_object_keys(item))<>2 OR jsonb_typeof(item->'event_id') IS DISTINCT FROM 'string' OR (item->>'event_id') !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
      OR jsonb_typeof(item->'generation') IS DISTINCT FROM 'number' OR (item->>'generation') !~ '^[0-9]+$' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_replay_preview'; END IF;
    IF (item->>'generation')::numeric>2147483646 THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_replay_preview'; END IF;
  END LOOP;
  IF (SELECT count(DISTINCT value->>'event_id') FROM jsonb_array_elements(reviewed))<>jsonb_array_length(reviewed) THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_replay_preview'; END IF;
  SELECT jsonb_agg(value ORDER BY value->>'event_id') INTO intended FROM jsonb_array_elements(reviewed);
  SELECT o.* INTO receipt FROM api.team_delivery_operations o WHERE o.tenant_id=team_id AND o.actor=caller_subject AND o.operation_id=batch_replay_team_deliveries_once.operation_id;
  IF FOUND THEN
    IF receipt.action<>'batch_replay' OR receipt.payload<>intended THEN RAISE SQLSTATE 'PT409' USING MESSAGE='delivery_payload_conflict'; END IF;
  ELSE
    FOR item IN SELECT value FROM jsonb_array_elements(intended) LOOP
      UPDATE api.team_order_outbox q SET state='pending',attempts=0,generation=q.generation+1,available_at=clock_timestamp(),lease_token=NULL,lease_until=NULL
        WHERE q.tenant_id=team_id AND q.event_id=(item->>'event_id')::uuid AND q.generation=(item->>'generation')::integer
        AND (q.state='dead' OR q.state='leased' AND q.attempts>=5 AND q.lease_until<=clock_timestamp()) RETURNING q.* INTO target;
      IF NOT FOUND THEN RAISE SQLSTATE 'PT409' USING MESSAGE='delivery_preview_stale'; END IF;
      UPDATE api.team_order_delivery_attempts a SET outcome='expired',finished_at=clock_timestamp() WHERE a.event_id=target.event_id AND a.attempt=target.total_attempts AND a.outcome='leased';
      results:=results||jsonb_build_array(jsonb_build_object('event_id',target.event_id,'generation',target.generation));
    END LOOP;
    INSERT INTO api.team_delivery_operations(operation_id,tenant_id,actor,action,payload,result) VALUES(operation_id,team_id,caller_subject,'batch_replay',intended,jsonb_build_object('replayed',results)) RETURNING * INTO receipt;
  END IF;
  PERFORM set_config('gregale.outbox_write',COALESCE(old_write,''),true);
  PERFORM set_config('gregale.delivery_control_write',COALESCE(old_control,''),true);
  RETURN NEXT receipt;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.outbox_write',COALESCE(old_write,''),true);
  PERFORM set_config('gregale.delivery_control_write',COALESCE(old_control,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.batch_replay_team_deliveries_once(text,uuid,jsonb) FROM PUBLIC;
COMMENT ON FUNCTION api.batch_replay_team_deliveries_once(text,uuid,jsonb) IS '@gregale:rpc';
