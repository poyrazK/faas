-- Organization audit log (ADR-1190): what happened in an organization, for
-- its owners (or roles granted audit:read). Triggers record membership,
-- invitation, domain and SSO changes with the acting user when the change
-- came through the organization API (the front sets request.jwt.claims);
-- changes the app's operator, an identity provider or SCIM made have no
-- actor. Members' sign-ins are read from GoTrue's audit log, not copied.
-- Entries go with their organization, and AUTH_AUDIT_RETENTION_DAYS prunes
-- them like GoTrue's log.
SET LOCAL lock_timeout = '5s';

CREATE TABLE gregale_auth.org_audit_log (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id uuid NOT NULL REFERENCES gregale_auth.organizations (id) ON DELETE CASCADE,
    -- clock_timestamp keeps several changes in one transaction in order.
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    actor_id uuid,
    action text NOT NULL CHECK (action IN ('organization.created', 'member.added', 'member.role_changed', 'member.removed',
        'invitation.created', 'invitation.revoked', 'invitation.accepted', 'domain.added', 'domain.verified', 'domain.removed',
        'sso.connected', 'sso.updated', 'sso.disconnected')),
    target_user_id uuid,
    data jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(data) = 'object')
);
CREATE INDEX org_audit_log_org_idx ON gregale_auth.org_audit_log (org_id, occurred_at DESC);
CREATE INDEX org_audit_log_time_idx ON gregale_auth.org_audit_log (occurred_at);

-- record_org_event skips an organization that is being deleted: its
-- entries would go with it, and the cascade must not fail.
CREATE FUNCTION gregale_auth.record_org_event(org uuid, event text, target uuid, details jsonb) RETURNS void
LANGUAGE sql SET search_path = '' AS $$
  INSERT INTO gregale_auth.org_audit_log (org_id, actor_id, action, target_user_id, data)
  SELECT org, gregale_auth.uid(), event, target, details WHERE EXISTS (SELECT 1 FROM gregale_auth.organizations o WHERE o.id = org)
$$;

CREATE FUNCTION gregale_auth.audit_org_change() RETURNS trigger
LANGUAGE plpgsql SET search_path = '' AS $$
BEGIN
  IF TG_TABLE_NAME = 'organizations' THEN
    PERFORM gregale_auth.record_org_event(NEW.id, 'organization.created', NULL, jsonb_build_object('slug', NEW.slug, 'name', NEW.name));
  ELSIF TG_TABLE_NAME = 'memberships' THEN
    IF TG_OP = 'INSERT' THEN
      PERFORM gregale_auth.record_org_event(NEW.org_id, 'member.added', NEW.user_id, jsonb_build_object('role', NEW.role));
    ELSIF TG_OP = 'UPDATE' THEN
      PERFORM gregale_auth.record_org_event(NEW.org_id, 'member.role_changed', NEW.user_id, jsonb_build_object('from', OLD.role, 'to', NEW.role));
    ELSE
      PERFORM gregale_auth.record_org_event(OLD.org_id, 'member.removed', OLD.user_id, jsonb_build_object('role', OLD.role,
        'account_deleted', NOT EXISTS (SELECT 1 FROM auth.users u WHERE u.id = OLD.user_id)));
    END IF;
  ELSIF TG_TABLE_NAME = 'invitations' THEN
    IF TG_OP = 'INSERT' THEN
      PERFORM gregale_auth.record_org_event(NEW.org_id, 'invitation.created', NULL, jsonb_build_object('email', NEW.email, 'role', NEW.role));
    ELSIF OLD.accepted_at IS NULL AND NEW.accepted_at IS NOT NULL THEN
      PERFORM gregale_auth.record_org_event(NEW.org_id, 'invitation.accepted', NEW.accepted_by, jsonb_build_object('email', NEW.email, 'role', NEW.role));
    ELSIF OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL THEN
      PERFORM gregale_auth.record_org_event(NEW.org_id, 'invitation.revoked', NULL, jsonb_build_object('email', NEW.email));
    END IF;
  ELSIF TG_TABLE_NAME = 'org_domains' THEN
    IF TG_OP = 'INSERT' THEN
      PERFORM gregale_auth.record_org_event(NEW.org_id, 'domain.added', NULL, jsonb_build_object('domain', NEW.domain));
    ELSIF TG_OP = 'UPDATE' THEN
      IF OLD.verified_at IS NULL AND NEW.verified_at IS NOT NULL THEN
        PERFORM gregale_auth.record_org_event(NEW.org_id, 'domain.verified', NULL, jsonb_build_object('domain', NEW.domain));
      END IF;
    ELSE
      PERFORM gregale_auth.record_org_event(OLD.org_id, 'domain.removed', NULL, jsonb_build_object('domain', OLD.domain));
    END IF;
  ELSIF TG_TABLE_NAME = 'sso_organizations' THEN
    IF TG_OP = 'INSERT' THEN
      PERFORM gregale_auth.record_org_event(NEW.org_id, 'sso.connected', NULL, jsonb_build_object('provider_id', NEW.provider_id, 'role', NEW.role));
    ELSIF TG_OP = 'DELETE' THEN
      PERFORM gregale_auth.record_org_event(OLD.org_id, 'sso.disconnected', NULL, jsonb_build_object('provider_id', OLD.provider_id));
    ELSIF OLD.org_id IS DISTINCT FROM NEW.org_id THEN
      PERFORM gregale_auth.record_org_event(OLD.org_id, 'sso.disconnected', NULL, jsonb_build_object('provider_id', OLD.provider_id));
      PERFORM gregale_auth.record_org_event(NEW.org_id, 'sso.connected', NULL, jsonb_build_object('provider_id', NEW.provider_id, 'role', NEW.role));
    ELSE
      PERFORM gregale_auth.record_org_event(NEW.org_id, 'sso.updated', NULL, jsonb_build_object('provider_id', NEW.provider_id, 'role', NEW.role));
    END IF;
  END IF;
  RETURN NULL;
END $$;

CREATE TRIGGER gregale_auth_audit_org AFTER INSERT ON gregale_auth.organizations
  FOR EACH ROW EXECUTE FUNCTION gregale_auth.audit_org_change();
CREATE TRIGGER gregale_auth_audit_memberships AFTER INSERT OR DELETE ON gregale_auth.memberships
  FOR EACH ROW EXECUTE FUNCTION gregale_auth.audit_org_change();
CREATE TRIGGER gregale_auth_audit_member_roles AFTER UPDATE OF role ON gregale_auth.memberships
  FOR EACH ROW WHEN (OLD.role IS DISTINCT FROM NEW.role) EXECUTE FUNCTION gregale_auth.audit_org_change();
CREATE TRIGGER gregale_auth_audit_invitations AFTER INSERT OR UPDATE OF accepted_at, revoked_at ON gregale_auth.invitations
  FOR EACH ROW EXECUTE FUNCTION gregale_auth.audit_org_change();
CREATE TRIGGER gregale_auth_audit_domains AFTER INSERT OR DELETE OR UPDATE OF verified_at ON gregale_auth.org_domains
  FOR EACH ROW EXECUTE FUNCTION gregale_auth.audit_org_change();
CREATE TRIGGER gregale_auth_audit_sso AFTER INSERT OR DELETE OR UPDATE ON gregale_auth.sso_organizations
  FOR EACH ROW EXECUTE FUNCTION gregale_auth.audit_org_change();

-- org_audit_feed is an organization's entries and its current members'
-- sign-ins, newest first, after the cursor (before_at, before_id): the
-- position of the previous page's last entry, at full precision, with the
-- entry's ID breaking ties. org_audit_entries is the same for a caller
-- holding audit:read.
CREATE FUNCTION gregale_auth.org_audit_feed(org uuid, before_at timestamptz, before_id uuid, max_rows integer)
RETURNS TABLE (id uuid, occurred_at timestamptz, action text, actor_id uuid, actor_email text, target_user_id uuid, target_email text, data jsonb)
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT * FROM (
    SELECT * FROM (
      SELECT l.id, l.occurred_at, l.action, l.actor_id, a.email::text, l.target_user_id, t.email::text, l.data
      FROM gregale_auth.org_audit_log l
        LEFT JOIN auth.users a ON a.id = l.actor_id
        LEFT JOIN auth.users t ON t.id = l.target_user_id
      WHERE l.org_id = org AND (before_at IS NULL OR (l.occurred_at, l.id) < (before_at, before_id))
      ORDER BY l.occurred_at DESC, l.id DESC LIMIT max_rows) recorded
    UNION ALL
    SELECT * FROM (
      SELECT e.id, e.created_at, 'member.signed_in'::text, m.user_id, u.email::text, m.user_id, u.email::text, '{}'::jsonb
      FROM auth.audit_log_entries e
        JOIN gregale_auth.memberships m ON m.org_id = org AND m.user_id::text = e.payload->>'actor_id'
        JOIN auth.users u ON u.id = m.user_id
      WHERE e.payload->>'action' = 'login' AND (before_at IS NULL OR (e.created_at, e.id) < (before_at, before_id))
      ORDER BY e.created_at DESC, e.id DESC LIMIT max_rows) signins
  ) feed ORDER BY 2 DESC, 1 DESC LIMIT max_rows
$$;

CREATE FUNCTION gregale_auth.org_audit_entries(org uuid, before_at timestamptz, before_id uuid, max_rows integer)
RETURNS TABLE (id uuid, occurred_at timestamptz, action text, actor_id uuid, actor_email text, target_user_id uuid, target_email text, data jsonb)
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_session();
  IF NOT gregale_auth.has_permission(org, 'audit:read') THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT * FROM gregale_auth.org_audit_feed(org, before_at, before_id, max_rows);
END $$;

REVOKE ALL ON FUNCTION gregale_auth.record_org_event(uuid, text, uuid, jsonb), gregale_auth.audit_org_change(),
  gregale_auth.org_audit_feed(uuid, timestamptz, uuid, integer), gregale_auth.org_audit_entries(uuid, timestamptz, uuid, integer) FROM PUBLIC;
