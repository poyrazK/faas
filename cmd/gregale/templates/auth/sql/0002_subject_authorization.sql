-- ADR-1190 phase 5: authorization for a subject the caller has already
-- verified but whose session it cannot name. Managed realtime passes only the
-- token's subject to its channel callback, so the check falls back from "this
-- session is active" to "this user is active": not banned, not deleted, and
-- holding at least one unexpired session. These functions trust their subject
-- argument, so only the auth owner (the Auth app's front) may call them.

CREATE FUNCTION gregale_auth.user_active(subject uuid) RETURNS boolean
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT EXISTS (
    SELECT 1 FROM auth.users u
    WHERE u.id = subject AND u.deleted_at IS NULL
      AND (u.banned_until IS NULL OR u.banned_until <= now())
      AND EXISTS (SELECT 1 FROM auth.sessions s WHERE s.user_id = u.id AND (s.not_after IS NULL OR s.not_after > now())))
$$;

CREATE FUNCTION gregale_auth.authorize_subject(subject uuid, requested_action text, resource text) RETURNS boolean
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  WITH parts AS (SELECT string_to_array(resource, '/') AS p),
  target AS (
    SELECT CASE WHEN p[2] ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' THEN p[2]::uuid END AS org, p[3] AS kind
    FROM parts
    WHERE cardinality(p) >= 4 AND p[1] = 'org' AND p[3] ~ '^[a-z][a-z0-9_-]{0,63}$' AND p[4] <> ''
  )
  SELECT gregale_auth.user_active(subject) AND coalesce((
    SELECT bool_or(EXISTS (
      SELECT 1 FROM gregale_auth.memberships m JOIN gregale_auth.roles r ON r.name = m.role
      WHERE m.org_id = t.org AND m.user_id = subject
        AND (pol.permission = ANY (r.permissions) OR '*' = ANY (r.permissions))))
    FROM target t JOIN gregale_auth.policies pol ON pol.resource_kind = t.kind AND pol.action = requested_action
    WHERE t.org IS NOT NULL), false)
$$;

REVOKE ALL ON FUNCTION gregale_auth.user_active(uuid), gregale_auth.authorize_subject(uuid, text, text) FROM PUBLIC;
