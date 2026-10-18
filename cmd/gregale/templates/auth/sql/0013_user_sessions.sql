-- A person's own sessions (ADR-1190): GoTrue v2.197.0 can end this session,
-- the others or all of them, but cannot list them or end one. Sessions of
-- third-party OAuth clients are left to connected apps. Like the other
-- owner-only functions, these run through the Auth app's front with
-- request.jwt.claims set from a token GoTrue accepted.
CREATE FUNCTION gregale_auth.list_sessions()
RETURNS TABLE (id uuid, created_at timestamptz, last_active_at timestamptz, user_agent text, ip text, aal text, current boolean)
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
DECLARE caller uuid := gregale_auth.require_session();
BEGIN
  -- refreshed_at is a UTC timestamp without a time zone.
  RETURN QUERY SELECT s.id, s.created_at, greatest(s.created_at, s.updated_at, s.refreshed_at AT TIME ZONE 'UTC'),
      left(s.user_agent, 512), host(s.ip), s.aal::text, s.id = gregale_auth.claim_uuid('session_id')
    FROM auth.sessions s
    WHERE s.user_id = caller AND s.oauth_client_id IS NULL AND (s.not_after IS NULL OR s.not_after > now())
    ORDER BY 3 DESC, s.id
    LIMIT 100;
END $$;

-- end_session signs one other session out; deleting it ends its refresh
-- tokens, and session_active() refuses its access tokens from then on.
-- The caller's own session ends through GoTrue's logout.
CREATE FUNCTION gregale_auth.end_session(target uuid) RETURNS void
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE caller uuid := gregale_auth.require_session();
BEGIN
  IF target = gregale_auth.claim_uuid('session_id') THEN
    RAISE EXCEPTION 'current_session' USING ERRCODE = '22023';
  END IF;
  DELETE FROM auth.sessions WHERE id = target AND user_id = caller AND oauth_client_id IS NULL;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
END $$;

REVOKE ALL ON FUNCTION gregale_auth.list_sessions(), gregale_auth.end_session(uuid) FROM PUBLIC;
