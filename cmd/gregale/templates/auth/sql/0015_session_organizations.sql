-- The organization a session acts in (ADR-1190). With AUTH_ORG_CLAIMS on,
-- the custom access token hook puts it, with the caller's role and that
-- role's permissions, into the session's access tokens. Ending the session
-- or deleting the organization forgets it; leaving the organization drops
-- the claims at the next token, because the hook reads the membership.
-- The foreign key locks auth.sessions against writes while it is added;
-- the release task runs beside the serving deployment, so it waits at most
-- five seconds for sign-ins in flight and fails rather than stall them.
SET LOCAL lock_timeout = '5s';

CREATE TABLE gregale_auth.session_organizations (
    session_id uuid PRIMARY KEY REFERENCES auth.sessions (id) ON DELETE CASCADE,
    org_id uuid NOT NULL REFERENCES gregale_auth.organizations (id) ON DELETE CASCADE,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- set_active_organization chooses an organization the caller belongs to for
-- the caller's session, or none with NULL. Like the other owner-only
-- functions it runs through the front under verified claims.
CREATE FUNCTION gregale_auth.set_active_organization(org uuid)
RETURNS TABLE (id uuid, slug text, role text)
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  caller uuid := gregale_auth.require_session();
  current_session uuid := gregale_auth.claim_uuid('session_id');
BEGIN
  IF org IS NULL THEN
    DELETE FROM gregale_auth.session_organizations s WHERE s.session_id = current_session;
    RETURN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM gregale_auth.memberships m WHERE m.org_id = org AND m.user_id = caller) THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  INSERT INTO gregale_auth.session_organizations AS s (session_id, org_id) VALUES (current_session, org)
    ON CONFLICT ON CONSTRAINT session_organizations_pkey DO UPDATE SET org_id = EXCLUDED.org_id, updated_at = now();
  RETURN QUERY SELECT o.id, o.slug, m.role FROM gregale_auth.organizations o
    JOIN gregale_auth.memberships m ON m.org_id = o.id AND m.user_id = caller WHERE o.id = org;
END $$;

REVOKE ALL ON FUNCTION gregale_auth.set_active_organization(uuid) FROM PUBLIC;
