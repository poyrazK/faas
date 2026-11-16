-- Optional outbox observation; invoker RLS admits only internal hooks claims.
CREATE OR REPLACE FUNCTION {{schema}}.gregale_change_status() RETURNS jsonb
LANGUAGE sql STABLE SECURITY INVOKER SET search_path=pg_catalog AS $$
 SELECT jsonb_build_object('pending',count(*) FILTER (WHERE failed_at IS NULL),
   'failed',count(*) FILTER (WHERE failed_at IS NOT NULL),
   'leased',count(*) FILTER (WHERE failed_at IS NULL AND claimed_until>clock_timestamp()),
   'oldest_pending_at',min(captured_at) FILTER (WHERE failed_at IS NULL),
   'lag_seconds',coalesce(greatest(0,extract(epoch FROM clock_timestamp()-min(captured_at) FILTER (WHERE failed_at IS NULL))),0))
 FROM {{schema}}.gregale_change_events
$$;
REVOKE ALL ON FUNCTION {{schema}}.gregale_change_status() FROM PUBLIC;
-- GRANT EXECUTE ON FUNCTION {{schema}}.gregale_change_status() TO your_data_api_login;
