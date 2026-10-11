ALTER TABLE api.outbox
  ADD COLUMN max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts BETWEEN 1 AND 1000),
  ADD COLUMN total_attempts bigint NOT NULL DEFAULT 0 CHECK (total_attempts >= 0),
  ADD COLUMN replay_generation integer NOT NULL DEFAULT 0 CHECK (replay_generation >= 0),
  ADD COLUMN dead_lettered boolean NOT NULL DEFAULT false,
  ADD CONSTRAINT dead_letter_state CHECK (NOT dead_lettered OR (NOT delivered AND lease_token IS NULL));
UPDATE api.outbox SET total_attempts=attempts;

CREATE OR REPLACE FUNCTION api.claim_event(claim_token uuid) RETURNS SETOF api.outbox
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF claim_token IS NULL OR COALESCE(current_setting('request.jwt.claims',true)::jsonb->>'outbox_worker','false')<>'true' THEN
    RAISE SQLSTATE 'PT403' USING MESSAGE='outbox_worker_required';
  END IF;
  -- A crash on the final attempt also exhausts the budget once its lease expires.
  UPDATE api.outbox e SET dead_lettered=true,lease_token=NULL,lease_until=NULL
    WHERE NOT e.delivered AND NOT e.dead_lettered AND e.attempts>=e.max_attempts
      AND (e.lease_until IS NULL OR e.lease_until<=clock_timestamp());
  RETURN QUERY WITH candidate AS (
    SELECT e.id FROM api.outbox e WHERE NOT e.delivered AND NOT e.dead_lettered
      AND e.attempts<e.max_attempts AND e.available_at<=clock_timestamp()
      AND (e.lease_until IS NULL OR e.lease_until<=clock_timestamp())
    ORDER BY e.available_at,e.id FOR UPDATE SKIP LOCKED LIMIT 1
  ) UPDATE api.outbox e SET attempts=e.attempts+1,total_attempts=e.total_attempts+1,lease_token=claim_token,
    lease_until=clock_timestamp()+interval '30 seconds'
    FROM candidate c WHERE e.id=c.id RETURNING e.*;
END;
$$;
CREATE OR REPLACE FUNCTION api.finish_event(event_id bigint,claim_token uuid,succeeded boolean) RETURNS SETOF api.outbox
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF claim_token IS NULL OR succeeded IS NULL OR COALESCE(current_setting('request.jwt.claims',true)::jsonb->>'outbox_worker','false')<>'true' THEN
    RAISE SQLSTATE 'PT403' USING MESSAGE='outbox_worker_required';
  END IF;
  RETURN QUERY UPDATE api.outbox e SET delivered=succeeded,
    dead_lettered=NOT succeeded AND e.attempts>=e.max_attempts,lease_token=NULL,lease_until=NULL,
    available_at=clock_timestamp()+make_interval(secs=>LEAST(300,power(2,LEAST(e.attempts,8))::integer))
    WHERE e.id=event_id AND e.lease_token=claim_token AND e.lease_until>clock_timestamp()
      AND NOT e.delivered AND NOT e.dead_lettered RETURNING e.*;
END;
$$;
CREATE FUNCTION api.replay_event(event_id bigint,expected_generation integer) RETURNS SETOF api.outbox
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  IF expected_generation IS NULL OR expected_generation<0 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_replay_generation';
  END IF;
  IF COALESCE(current_setting('request.jwt.claims',true)::jsonb->>'outbox_worker','false')<>'true' THEN
    RAISE SQLSTATE 'PT403' USING MESSAGE='outbox_worker_required';
  END IF;
  RETURN QUERY UPDATE api.outbox e SET attempts=0,replay_generation=e.replay_generation+1,
    dead_lettered=false,lease_token=NULL,lease_until=NULL,available_at=clock_timestamp()
    WHERE e.id=event_id AND e.replay_generation=expected_generation AND NOT e.delivered
      AND (e.dead_lettered OR (e.attempts>=e.max_attempts AND (e.lease_until IS NULL OR e.lease_until<=clock_timestamp())))
    RETURNING e.*;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.replay_event(bigint,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.replay_event(bigint,integer) IS '@gregale:rpc';

-- Invoker rights apply the querying identity's RLS policies to aggregates too.
CREATE VIEW api.outbox_health WITH (security_invoker=true) AS
SELECT count(*) FILTER(WHERE state='pending') AS pending,
       count(*) FILTER(WHERE state='leased') AS leased,
       count(*) FILTER(WHERE state='delivered') AS delivered,
       count(*) FILTER(WHERE state='dead-lettered') AS dead_lettered
FROM (
 SELECT CASE WHEN e.delivered THEN 'delivered'
   WHEN e.dead_lettered THEN 'dead-lettered'
   WHEN e.lease_until>statement_timestamp() THEN 'leased'
   WHEN e.attempts>=e.max_attempts THEN 'dead-lettered'
   ELSE 'pending' END AS state FROM api.outbox e
) statuses;
