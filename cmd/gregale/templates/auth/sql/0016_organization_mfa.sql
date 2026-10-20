-- Organizations that require two-step verification (ADR-1190). Someone
-- holding mfa:manage (owners hold every permission) turns it on from a
-- session verified with a second factor. From then on the organization's
-- permissions hold only in sessions at aal2: every check that goes through
-- has_permission (authorize() in row-level security, storage, workflows,
-- realtime and the organization endpoints) applies it, and the access token
-- hook leaves the organization's claims out below aal2. Members without a
-- factor stay members and regain access once they enrol and verify one.
-- The new column has a constant default, so adding it rewrites nothing; the
-- release task waits at most five seconds for the organizations table.
SET LOCAL lock_timeout = '5s';

ALTER TABLE gregale_auth.organizations ADD COLUMN mfa_required boolean NOT NULL DEFAULT false;

-- session_mfa is true when the caller's session was verified with a second
-- factor. It reads the session row rather than the token's aal claim:
-- GoTrue lowers a session to aal1 when its factor is removed, while tokens
-- issued before keep saying aal2 until they expire.
CREATE FUNCTION gregale_auth.session_mfa() RETURNS boolean
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT EXISTS (
    SELECT 1 FROM auth.sessions s
    WHERE s.id = gregale_auth.claim_uuid('session_id') AND s.user_id = gregale_auth.uid() AND s.aal::text = 'aal2')
$$;

CREATE OR REPLACE FUNCTION gregale_auth.has_permission(org uuid, required_permission text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
  SELECT gregale_auth.session_active() AND EXISTS (
    SELECT 1 FROM gregale_auth.memberships m
      JOIN gregale_auth.roles r ON r.name = m.role
      JOIN gregale_auth.organizations o ON o.id = m.org_id
    WHERE m.org_id = org AND m.user_id = gregale_auth.uid()
      AND (required_permission = ANY (r.permissions) OR '*' = ANY (r.permissions))
      AND (NOT o.mfa_required OR gregale_auth.session_mfa()))
$$;
COMMENT ON FUNCTION gregale_auth.has_permission(uuid, text) IS 'gregale:auth-helper:v1';

-- The subject-level check cannot see a session, so in an organization that
-- requires a second factor it needs the user to hold a live aal2 session.
CREATE OR REPLACE FUNCTION gregale_auth.authorize_subject(subject uuid, requested_action text, resource text) RETURNS boolean
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  WITH parts AS (SELECT string_to_array(resource, '/') AS p),
  target AS (
    SELECT CASE WHEN p[2] ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' THEN p[2]::uuid END AS org, p[3] AS kind
    FROM parts
    WHERE cardinality(p) >= 4 AND p[1] = 'org' AND p[3] ~ '^[a-z][a-z0-9_-]{0,63}$' AND p[4] <> ''
  )
  SELECT gregale_auth.user_active(subject) AND coalesce((
    SELECT bool_or(EXISTS (
      SELECT 1 FROM gregale_auth.memberships m
        JOIN gregale_auth.roles r ON r.name = m.role
        JOIN gregale_auth.organizations o ON o.id = m.org_id
      WHERE m.org_id = t.org AND m.user_id = subject
        AND (pol.permission = ANY (r.permissions) OR '*' = ANY (r.permissions))
        AND (NOT o.mfa_required OR EXISTS (
          SELECT 1 FROM auth.sessions s
          WHERE s.user_id = subject AND s.aal::text = 'aal2' AND (s.not_after IS NULL OR s.not_after > now())))))
    FROM target t JOIN gregale_auth.policies pol ON pol.resource_kind = t.kind AND pol.action = requested_action
    WHERE t.org IS NOT NULL), false)
$$;

-- mfa_blocks tells a member why the organization refused them: it requires
-- a second factor this session has not verified. The front asks only after
-- a refusal, so non-members learn nothing new.
CREATE FUNCTION gregale_auth.mfa_blocks(org uuid) RETURNS boolean
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT EXISTS (
    SELECT 1 FROM gregale_auth.memberships m JOIN gregale_auth.organizations o ON o.id = m.org_id
    WHERE m.org_id = org AND m.user_id = gregale_auth.uid() AND o.mfa_required) AND NOT gregale_auth.session_mfa()
$$;

-- set_organization_mfa turns the requirement on or off. The caller's own
-- session must be at aal2 either way: turning it on cannot lock the caller
-- out, and a session without the second factor cannot turn it off.
CREATE FUNCTION gregale_auth.set_organization_mfa(org uuid, required boolean)
RETURNS TABLE (id uuid, slug text, name text, mfa_required boolean)
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_session();
  PERFORM gregale_auth.lock_organization(org, 'mfa:manage');
  IF NOT gregale_auth.session_mfa() THEN
    RAISE EXCEPTION 'mfa_required' USING ERRCODE = '42501';
  END IF;
  IF required IS NULL THEN
    RAISE EXCEPTION 'invalid_organization' USING ERRCODE = '22023';
  END IF;
  UPDATE gregale_auth.organizations o SET mfa_required = required WHERE o.id = org;
  RETURN QUERY SELECT o.id, o.slug, o.name, o.mfa_required FROM gregale_auth.organizations o WHERE o.id = org;
END $$;

-- Members still see an organization that requires a second factor, so an
-- application can tell them why it refuses them.
DROP FUNCTION gregale_auth.list_organizations();
CREATE FUNCTION gregale_auth.list_organizations()
RETURNS TABLE (id uuid, slug text, name text, role text, created_at timestamptz, mfa_required boolean)
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
DECLARE caller uuid := gregale_auth.require_session();
BEGIN
  RETURN QUERY SELECT o.id, o.slug, o.name, m.role, o.created_at, o.mfa_required
    FROM gregale_auth.memberships m JOIN gregale_auth.organizations o ON o.id = m.org_id
    WHERE m.user_id = caller ORDER BY o.created_at, o.id;
END $$;

-- Whether each member has a verified second factor is shown only to those
-- who manage members, so they can see who a requirement would shut out.
DROP FUNCTION gregale_auth.list_members(uuid);
CREATE FUNCTION gregale_auth.list_members(org uuid)
RETURNS TABLE (user_id uuid, email text, role text, created_at timestamptz, mfa_enabled boolean)
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
DECLARE manager boolean;
BEGIN
  PERFORM gregale_auth.require_session();
  IF NOT gregale_auth.has_permission(org, 'org:read') THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  manager := gregale_auth.has_permission(org, 'members:manage');
  RETURN QUERY SELECT m.user_id, u.email::text, m.role, m.created_at,
      CASE WHEN manager THEN EXISTS (
        SELECT 1 FROM auth.mfa_factors f
        WHERE f.user_id = m.user_id AND f.status::text = 'verified' AND f.factor_type::text <> 'recovery_code') END
    FROM gregale_auth.memberships m JOIN auth.users u ON u.id = m.user_id
    WHERE m.org_id = org ORDER BY m.created_at, m.user_id;
END $$;

-- A session acts in an organization that requires a second factor only once
-- it has verified one; until then the choice is refused rather than kept.
CREATE OR REPLACE FUNCTION gregale_auth.set_active_organization(org uuid)
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
  IF gregale_auth.mfa_blocks(org) THEN
    RAISE EXCEPTION 'mfa_required' USING ERRCODE = '42501';
  END IF;
  INSERT INTO gregale_auth.session_organizations AS s (session_id, org_id) VALUES (current_session, org)
    ON CONFLICT ON CONSTRAINT session_organizations_pkey DO UPDATE SET org_id = EXCLUDED.org_id, updated_at = now();
  RETURN QUERY SELECT o.id, o.slug, m.role FROM gregale_auth.organizations o
    JOIN gregale_auth.memberships m ON m.org_id = o.id AND m.user_id = caller WHERE o.id = org;
END $$;

REVOKE ALL ON FUNCTION gregale_auth.session_mfa(), gregale_auth.mfa_blocks(uuid), gregale_auth.set_organization_mfa(uuid, boolean),
  gregale_auth.list_organizations(), gregale_auth.list_members(uuid) FROM PUBLIC;
