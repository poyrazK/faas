-- ADR-1190 phase 3: organizations, memberships, roles, invitations and the
-- authorization function every surface shares. migrate.mjs applies this in
-- the release task as the owner of the auth and gregale_auth schemas, after
-- GoTrue's migrations. Customer schemas are never touched.

CREATE TABLE gregale_auth.roles (
  name text PRIMARY KEY CHECK (name ~ '^[a-z][a-z0-9_-]{0,31}$'),
  permissions text[] NOT NULL DEFAULT '{}' CHECK (cardinality(permissions) <= 64),
  builtin boolean NOT NULL DEFAULT false
);
INSERT INTO gregale_auth.roles (name, permissions, builtin) VALUES
  ('owner', '{*}', true),
  ('admin', '{members:manage,org:read}', true),
  ('member', '{org:read}', true);

CREATE TABLE gregale_auth.organizations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
  created_by uuid REFERENCES auth.users (id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX organizations_created_by_idx ON gregale_auth.organizations (created_by);

CREATE TABLE gregale_auth.memberships (
  org_id uuid NOT NULL REFERENCES gregale_auth.organizations (id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES auth.users (id) ON DELETE CASCADE,
  role text NOT NULL REFERENCES gregale_auth.roles (name),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, user_id)
);
CREATE INDEX memberships_user_idx ON gregale_auth.memberships (user_id);

-- Only a SHA-256 of each invitation token is stored; the token is shown once.
CREATE TABLE gregale_auth.invitations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id uuid NOT NULL REFERENCES gregale_auth.organizations (id) ON DELETE CASCADE,
  email text NOT NULL CHECK (char_length(email) BETWEEN 3 AND 320 AND email = lower(email)),
  role text NOT NULL REFERENCES gregale_auth.roles (name),
  token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
  invited_by uuid REFERENCES auth.users (id) ON DELETE SET NULL,
  expires_at timestamptz NOT NULL,
  accepted_at timestamptz,
  accepted_by uuid REFERENCES auth.users (id) ON DELETE SET NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (accepted_at IS NULL OR revoked_at IS NULL)
);
CREATE INDEX invitations_org_idx ON gregale_auth.invitations (org_id);

-- policies map an action on a kind of resource to the permission it needs.
CREATE TABLE gregale_auth.policies (
  action text NOT NULL CHECK (action ~ '^[a-z][a-z0-9_.:-]{0,63}$'),
  resource_kind text NOT NULL CHECK (resource_kind ~ '^[a-z][a-z0-9_-]{0,63}$'),
  permission text NOT NULL CHECK (permission ~ '^[a-z][a-z0-9_.:-]{0,63}$'),
  PRIMARY KEY (action, resource_kind, permission)
);

-- Claim readers. They read only the caller's own request.jwt.claims, which
-- the Data API sets from a verified token, so they need no special rights.
CREATE FUNCTION gregale_auth.claims() RETURNS jsonb
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT coalesce(nullif(current_setting('request.jwt.claims', true), '')::jsonb, '{}'::jsonb)
$$;

CREATE FUNCTION gregale_auth.claim_uuid(claim text) RETURNS uuid
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT CASE WHEN v ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' THEN v::uuid END
  FROM (SELECT gregale_auth.claims() ->> claim AS v) c
$$;

CREATE FUNCTION gregale_auth.uid() RETURNS uuid
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT gregale_auth.claim_uuid('sub')
$$;

-- Authorization helpers. SECURITY DEFINER lets any caller learn facts about
-- its own verified session and memberships without reading these tables.
-- They are STABLE SQL, so PostgreSQL runs them read-only; the marker lets the
-- read-only credential verifier accept them and nothing else (ADR-1190).
CREATE FUNCTION gregale_auth.session_active() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
  SELECT EXISTS (
    SELECT 1 FROM auth.sessions s JOIN auth.users u ON u.id = s.user_id
    WHERE s.id = gregale_auth.claim_uuid('session_id') AND s.user_id = gregale_auth.uid()
      AND (s.not_after IS NULL OR s.not_after > now())
      AND (u.banned_until IS NULL OR u.banned_until <= now())
      AND u.deleted_at IS NULL)
$$;

CREATE FUNCTION gregale_auth.has_permission(org uuid, required_permission text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
  SELECT gregale_auth.session_active() AND EXISTS (
    SELECT 1 FROM gregale_auth.memberships m JOIN gregale_auth.roles r ON r.name = m.role
    WHERE m.org_id = org AND m.user_id = gregale_auth.uid()
      AND (required_permission = ANY (r.permissions) OR '*' = ANY (r.permissions)))
$$;

-- authorize(action, 'org/<uuid>/<kind>/<name>') denies by default: it allows
-- an action only when a policy maps it on that kind of resource to a
-- permission the caller holds in that organization.
CREATE FUNCTION gregale_auth.authorize(requested_action text, resource text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
  WITH parts AS (SELECT string_to_array(resource, '/') AS p),
  target AS (
    SELECT CASE WHEN p[2] ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' THEN p[2]::uuid END AS org, p[3] AS kind
    FROM parts
    WHERE cardinality(p) >= 4 AND p[1] = 'org' AND p[3] ~ '^[a-z][a-z0-9_-]{0,63}$' AND p[4] <> ''
  )
  SELECT coalesce((
    SELECT bool_or(gregale_auth.has_permission(t.org, pol.permission))
    FROM target t JOIN gregale_auth.policies pol ON pol.resource_kind = t.kind AND pol.action = requested_action
    WHERE t.org IS NOT NULL), false)
$$;

COMMENT ON FUNCTION gregale_auth.session_active() IS 'gregale:auth-helper:v1';
COMMENT ON FUNCTION gregale_auth.has_permission(uuid, text) IS 'gregale:auth-helper:v1';
COMMENT ON FUNCTION gregale_auth.authorize(text, text) IS 'gregale:auth-helper:v1';
GRANT USAGE ON SCHEMA gregale_auth TO PUBLIC;
GRANT EXECUTE ON FUNCTION gregale_auth.claims(), gregale_auth.claim_uuid(text), gregale_auth.uid(),
  gregale_auth.session_active(), gregale_auth.has_permission(uuid, text), gregale_auth.authorize(text, text) TO PUBLIC;

-- Mutations below run only as the auth owner (the Auth app's front), which
-- sets request.jwt.claims from a token GoTrue accepted. Errors carry a stable
-- code in the message and a SQLSTATE the front maps to an HTTP status.
CREATE FUNCTION gregale_auth.require_session() RETURNS uuid
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF gregale_auth.uid() IS NULL OR NOT gregale_auth.session_active() THEN
    RAISE EXCEPTION 'not_authenticated' USING ERRCODE = '28000';
  END IF;
  RETURN gregale_auth.uid();
END $$;

CREATE FUNCTION gregale_auth.caller_role(org uuid) RETURNS text
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT role FROM gregale_auth.memberships WHERE org_id = org AND user_id = gregale_auth.uid()
$$;

-- Serializes membership changes in one organization so concurrent demotions
-- cannot remove its last owner. Missing organizations read as forbidden.
CREATE FUNCTION gregale_auth.lock_organization(org uuid, required_permission text) RETURNS void
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM 1 FROM gregale_auth.organizations WHERE id = org FOR UPDATE;
  IF NOT FOUND OR (required_permission IS NOT NULL AND NOT gregale_auth.has_permission(org, required_permission)) THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
END $$;

CREATE FUNCTION gregale_auth.create_organization(org_slug text, org_name text) RETURNS gregale_auth.organizations
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  caller uuid := gregale_auth.require_session();
  created gregale_auth.organizations;
BEGIN
  IF (SELECT count(*) FROM gregale_auth.organizations WHERE created_by = caller) >= 100 THEN
    RAISE EXCEPTION 'organization_limit' USING ERRCODE = '54000';
  END IF;
  INSERT INTO gregale_auth.organizations (slug, name, created_by) VALUES (org_slug, org_name, caller) RETURNING * INTO created;
  INSERT INTO gregale_auth.memberships (org_id, user_id, role) VALUES (created.id, caller, 'owner');
  RETURN created;
EXCEPTION
  WHEN unique_violation THEN RAISE EXCEPTION 'slug_taken' USING ERRCODE = '23505';
  WHEN check_violation THEN RAISE EXCEPTION 'invalid_organization' USING ERRCODE = '22023';
END $$;

CREATE FUNCTION gregale_auth.list_organizations()
RETURNS TABLE (id uuid, slug text, name text, role text, created_at timestamptz)
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
DECLARE caller uuid := gregale_auth.require_session();
BEGIN
  RETURN QUERY SELECT o.id, o.slug, o.name, m.role, o.created_at
    FROM gregale_auth.memberships m JOIN gregale_auth.organizations o ON o.id = m.org_id
    WHERE m.user_id = caller ORDER BY o.created_at, o.id;
END $$;

CREATE FUNCTION gregale_auth.list_members(org uuid)
RETURNS TABLE (user_id uuid, email text, role text, created_at timestamptz)
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_session();
  IF NOT gregale_auth.has_permission(org, 'org:read') THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT m.user_id, u.email::text, m.role, m.created_at
    FROM gregale_auth.memberships m JOIN auth.users u ON u.id = m.user_id
    WHERE m.org_id = org ORDER BY m.created_at, m.user_id;
END $$;

CREATE FUNCTION gregale_auth.invite(org uuid, invitee_email text, invitee_role text, hashed_token bytea, ttl_seconds integer)
RETURNS gregale_auth.invitations
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  caller uuid := gregale_auth.require_session();
  created gregale_auth.invitations;
BEGIN
  PERFORM gregale_auth.lock_organization(org, 'members:manage');
  IF NOT EXISTS (SELECT 1 FROM gregale_auth.roles WHERE name = invitee_role) THEN
    RAISE EXCEPTION 'unknown_role' USING ERRCODE = '22023';
  END IF;
  IF invitee_role = 'owner' AND gregale_auth.caller_role(org) IS DISTINCT FROM 'owner' THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  IF ttl_seconds NOT BETWEEN 300 AND 1209600 THEN
    RAISE EXCEPTION 'invalid_invitation' USING ERRCODE = '22023';
  END IF;
  IF (SELECT count(*) FROM gregale_auth.invitations
      WHERE org_id = org AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()) >= 500 THEN
    RAISE EXCEPTION 'invitation_limit' USING ERRCODE = '54000';
  END IF;
  INSERT INTO gregale_auth.invitations (org_id, email, role, token_hash, invited_by, expires_at)
    VALUES (org, lower(btrim(invitee_email)), invitee_role, hashed_token, caller, now() + make_interval(secs => ttl_seconds))
    RETURNING * INTO created;
  RETURN created;
EXCEPTION
  WHEN check_violation THEN RAISE EXCEPTION 'invalid_invitation' USING ERRCODE = '22023';
END $$;

CREATE FUNCTION gregale_auth.list_invitations(org uuid)
RETURNS TABLE (id uuid, email text, role text, expires_at timestamptz, created_at timestamptz)
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_session();
  IF NOT gregale_auth.has_permission(org, 'members:manage') THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT i.id, i.email, i.role, i.expires_at, i.created_at FROM gregale_auth.invitations i
    WHERE i.org_id = org AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > now()
    ORDER BY i.created_at, i.id;
END $$;

CREATE FUNCTION gregale_auth.revoke_invitation(org uuid, invitation uuid) RETURNS void
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_session();
  PERFORM gregale_auth.lock_organization(org, 'members:manage');
  UPDATE gregale_auth.invitations SET revoked_at = now()
    WHERE id = invitation AND org_id = org AND accepted_at IS NULL AND revoked_at IS NULL;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
END $$;

-- An invitation binds to the invited address: only a signed-in user whose
-- confirmed email matches it can accept, and only once.
CREATE FUNCTION gregale_auth.accept_invitation(hashed_token bytea) RETURNS gregale_auth.memberships
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  caller uuid := gregale_auth.require_session();
  pending gregale_auth.invitations;
  joined gregale_auth.memberships;
BEGIN
  SELECT * INTO pending FROM gregale_auth.invitations WHERE token_hash = hashed_token FOR UPDATE;
  IF NOT FOUND OR pending.accepted_at IS NOT NULL OR pending.revoked_at IS NOT NULL OR pending.expires_at <= now() THEN
    RAISE EXCEPTION 'invitation_invalid' USING ERRCODE = 'P0002';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM auth.users u WHERE u.id = caller AND lower(u.email) = pending.email AND u.email_confirmed_at IS NOT NULL) THEN
    RAISE EXCEPTION 'invitation_email_mismatch' USING ERRCODE = '42501';
  END IF;
  PERFORM 1 FROM gregale_auth.organizations WHERE id = pending.org_id FOR UPDATE;
  INSERT INTO gregale_auth.memberships (org_id, user_id, role) VALUES (pending.org_id, caller, pending.role)
    ON CONFLICT (org_id, user_id) DO NOTHING RETURNING * INTO joined;
  IF joined IS NULL THEN
    RAISE EXCEPTION 'already_member' USING ERRCODE = '23505';
  END IF;
  UPDATE gregale_auth.invitations SET accepted_at = now(), accepted_by = caller WHERE id = pending.id;
  RETURN joined;
END $$;

CREATE FUNCTION gregale_auth.set_member_role(org uuid, member uuid, new_role text) RETURNS gregale_auth.memberships
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  existing gregale_auth.memberships;
  updated gregale_auth.memberships;
BEGIN
  PERFORM gregale_auth.require_session();
  PERFORM gregale_auth.lock_organization(org, 'members:manage');
  SELECT * INTO existing FROM gregale_auth.memberships WHERE org_id = org AND user_id = member;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM gregale_auth.roles WHERE name = new_role) THEN
    RAISE EXCEPTION 'unknown_role' USING ERRCODE = '22023';
  END IF;
  IF (existing.role = 'owner' OR new_role = 'owner') AND gregale_auth.caller_role(org) IS DISTINCT FROM 'owner' THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  IF existing.role = 'owner' AND new_role <> 'owner'
     AND (SELECT count(*) FROM gregale_auth.memberships WHERE org_id = org AND role = 'owner') = 1 THEN
    RAISE EXCEPTION 'last_owner' USING ERRCODE = '23514';
  END IF;
  UPDATE gregale_auth.memberships SET role = new_role WHERE org_id = org AND user_id = member RETURNING * INTO updated;
  RETURN updated;
END $$;

-- Members may always leave; removing someone else needs members:manage, and
-- only owners remove owners. No organization may lose its last owner.
CREATE FUNCTION gregale_auth.remove_member(org uuid, member uuid) RETURNS void
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  caller uuid := gregale_auth.require_session();
  existing gregale_auth.memberships;
BEGIN
  PERFORM gregale_auth.lock_organization(org, CASE WHEN member = caller THEN NULL ELSE 'members:manage' END);
  SELECT * INTO existing FROM gregale_auth.memberships WHERE org_id = org AND user_id = member;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  IF existing.role = 'owner' AND member <> caller AND gregale_auth.caller_role(org) IS DISTINCT FROM 'owner' THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  IF existing.role = 'owner' AND (SELECT count(*) FROM gregale_auth.memberships WHERE org_id = org AND role = 'owner') = 1 THEN
    RAISE EXCEPTION 'last_owner' USING ERRCODE = '23514';
  END IF;
  DELETE FROM gregale_auth.memberships WHERE org_id = org AND user_id = member;
END $$;

-- apply_authz replaces custom roles and every policy from the owner's
-- declarative AUTH_AUTHZ document. Built-in roles keep their names; owner
-- always holds every permission. A role still assigned cannot be removed.
CREATE FUNCTION gregale_auth.apply_authz(config jsonb) RETURNS void
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  role_entry record;
  entry jsonb;
  perms text[];
BEGIN
  IF jsonb_typeof(config) IS DISTINCT FROM 'object' OR EXISTS (SELECT 1 FROM jsonb_object_keys(config) k WHERE k NOT IN ('roles', 'policies'))
     OR jsonb_typeof(coalesce(config -> 'roles', '{}')) <> 'object' OR jsonb_typeof(coalesce(config -> 'policies', '[]')) <> 'array'
     OR (SELECT count(*) FROM jsonb_object_keys(coalesce(config -> 'roles', '{}'))) > 32
     OR jsonb_array_length(coalesce(config -> 'policies', '[]')) > 500 THEN
    RAISE EXCEPTION 'invalid_authz' USING ERRCODE = '22023';
  END IF;
  IF (config -> 'roles') ? 'owner' THEN
    RAISE EXCEPTION 'owner_role_fixed' USING ERRCODE = '22023';
  END IF;
  FOR role_entry IN SELECT key, value FROM jsonb_each(coalesce(config -> 'roles', '{}')) LOOP
    IF jsonb_typeof(role_entry.value) <> 'array' OR EXISTS (
        SELECT 1 FROM jsonb_array_elements(role_entry.value) e
        WHERE jsonb_typeof(e) <> 'string' OR NOT (e #>> '{}' ~ '^([a-z][a-z0-9_.:-]{0,63}|\*)$')) THEN
      RAISE EXCEPTION 'invalid_authz' USING ERRCODE = '22023';
    END IF;
    perms := ARRAY(SELECT DISTINCT e FROM jsonb_array_elements_text(role_entry.value) e ORDER BY e);
    INSERT INTO gregale_auth.roles (name, permissions) VALUES (role_entry.key, perms)
      ON CONFLICT (name) DO UPDATE SET permissions = excluded.permissions;
  END LOOP;
  DELETE FROM gregale_auth.roles r
    WHERE NOT r.builtin AND NOT (coalesce(config -> 'roles', '{}') ? r.name);
  DELETE FROM gregale_auth.policies;
  FOR entry IN SELECT value FROM jsonb_array_elements(coalesce(config -> 'policies', '[]')) LOOP
    IF jsonb_typeof(entry) <> 'object' OR EXISTS (SELECT 1 FROM jsonb_object_keys(entry) k WHERE k NOT IN ('action', 'resource', 'permission')) THEN
      RAISE EXCEPTION 'invalid_authz' USING ERRCODE = '22023';
    END IF;
    INSERT INTO gregale_auth.policies (action, resource_kind, permission)
      VALUES (entry ->> 'action', entry ->> 'resource', entry ->> 'permission') ON CONFLICT DO NOTHING;
  END LOOP;
EXCEPTION
  WHEN foreign_key_violation THEN RAISE EXCEPTION 'role_in_use' USING ERRCODE = '23503';
  WHEN check_violation OR not_null_violation THEN RAISE EXCEPTION 'invalid_authz' USING ERRCODE = '22023';
END $$;

REVOKE ALL ON FUNCTION gregale_auth.require_session(), gregale_auth.caller_role(uuid), gregale_auth.lock_organization(uuid, text),
  gregale_auth.create_organization(text, text), gregale_auth.list_organizations(), gregale_auth.list_members(uuid),
  gregale_auth.invite(uuid, text, text, bytea, integer), gregale_auth.list_invitations(uuid), gregale_auth.revoke_invitation(uuid, uuid),
  gregale_auth.accept_invitation(bytea), gregale_auth.set_member_role(uuid, uuid, text), gregale_auth.remove_member(uuid, uuid),
  gregale_auth.apply_authz(jsonb) FROM PUBLIC;
