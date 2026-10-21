-- Service accounts (ADR-1190): an organization's machine principals. A
-- backend trades a service account's ID and one of its secrets for an access
-- token at the OAuth token endpoint (client-credentials grant); the front
-- signs it with the app's key and marks it service_account, with the secret
-- it used in secret_id. A service account is not a GoTrue user and holds one
-- of the organization's roles. Secrets are stored as SHA-256 digests only.
-- The audit log's new action constraint is a superset of the old one, which
-- every existing row satisfies, so it is added without a validating scan.
SET LOCAL lock_timeout = '5s';

CREATE TABLE gregale_auth.service_accounts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id uuid NOT NULL REFERENCES gregale_auth.organizations (id) ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100 AND name !~ '[[:cntrl:]]'),
  role text NOT NULL REFERENCES gregale_auth.roles (name),
  disabled_at timestamptz,
  created_by uuid REFERENCES auth.users (id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX service_accounts_org_idx ON gregale_auth.service_accounts (org_id, created_at);
CREATE INDEX service_accounts_role_idx ON gregale_auth.service_accounts (role);

CREATE TABLE gregale_auth.service_account_secrets (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  service_account_id uuid NOT NULL REFERENCES gregale_auth.service_accounts (id) ON DELETE CASCADE,
  secret_hash bytea NOT NULL UNIQUE CHECK (octet_length(secret_hash) = 32),
  -- The secret's last four characters, so people can tell secrets apart.
  hint text NOT NULL CHECK (hint ~ '^[A-Za-z0-9_-]{4}$'),
  created_by uuid REFERENCES auth.users (id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz,
  last_used_at timestamptz,
  revoked_at timestamptz,
  CHECK (expires_at IS NULL OR expires_at > created_at)
);
CREATE INDEX service_account_secrets_account_idx ON gregale_auth.service_account_secrets (service_account_id);

-- service_account_active is true for a service-account token whose account
-- exists and is enabled and whose secret is neither revoked nor expired, so
-- deleting, disabling or revoking takes effect at the next check.
CREATE FUNCTION gregale_auth.service_account_active() RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
  SELECT gregale_auth.claims() -> 'service_account' = 'true'::jsonb AND EXISTS (
    SELECT 1 FROM gregale_auth.service_account_secrets k JOIN gregale_auth.service_accounts a ON a.id = k.service_account_id
    WHERE k.id = gregale_auth.claim_uuid('secret_id') AND a.id = gregale_auth.uid() AND a.disabled_at IS NULL
      AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at > now()))
$$;

-- has_permission grants a service account its role's permissions in its own
-- organization, within the token's scope when it was narrowed. A token
-- carrying the service_account claim never takes the people's branch, so it
-- never satisfies session_active(); organizations that require a second
-- factor apply it to the people who manage service accounts.
CREATE OR REPLACE FUNCTION gregale_auth.has_permission(org uuid, required_permission text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
  SELECT CASE WHEN gregale_auth.claims() ? 'service_account' THEN
    gregale_auth.service_account_active() AND EXISTS (
      SELECT 1 FROM gregale_auth.service_accounts a JOIN gregale_auth.roles r ON r.name = a.role
      WHERE a.id = gregale_auth.uid() AND a.org_id = org
        AND (required_permission = ANY (r.permissions) OR '*' = ANY (r.permissions))
        AND (NOT (gregale_auth.claims() ? 'scope')
          OR required_permission = ANY (string_to_array(gregale_auth.claims() ->> 'scope', ' '))))
  ELSE
    gregale_auth.session_active() AND EXISTS (
      SELECT 1 FROM gregale_auth.memberships m
        JOIN gregale_auth.roles r ON r.name = m.role
        JOIN gregale_auth.organizations o ON o.id = m.org_id
      WHERE m.org_id = org AND m.user_id = gregale_auth.uid()
        AND (required_permission = ANY (r.permissions) OR '*' = ANY (r.permissions))
        AND (NOT o.mfa_required OR gregale_auth.session_mfa()))
  END
$$;

COMMENT ON FUNCTION gregale_auth.has_permission(uuid, text) IS 'gregale:auth-helper:v1';
COMMENT ON FUNCTION gregale_auth.service_account_active() IS 'gregale:auth-helper:v1';
GRANT EXECUTE ON FUNCTION gregale_auth.service_account_active() TO PUBLIC;

-- service_account_rows lists service accounts with their live secrets, for
-- one organization or, for the app's operator, every one.
CREATE FUNCTION gregale_auth.service_account_rows(org uuid, max_rows integer)
RETURNS TABLE (id uuid, org_id uuid, name text, role text, disabled boolean, created_at timestamptz, secrets jsonb)
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT a.id, a.org_id, a.name, a.role, a.disabled_at IS NOT NULL, a.created_at, coalesce((
    SELECT jsonb_agg(jsonb_build_object('id', k.id, 'hint', k.hint, 'created_at', k.created_at, 'expires_at', k.expires_at,
        'last_used_at', k.last_used_at) ORDER BY k.created_at, k.id)
    FROM gregale_auth.service_account_secrets k
    WHERE k.service_account_id = a.id AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at > now())), '[]'::jsonb)
  FROM gregale_auth.service_accounts a
  WHERE org IS NULL OR a.org_id = org
  ORDER BY a.created_at, a.id
  LIMIT max_rows
$$;

-- add_service_account_secret stores a new secret for an account that has
-- fewer than two live ones; two let a backend switch without downtime.
CREATE FUNCTION gregale_auth.add_service_account_secret(account uuid, hashed_secret bytea, secret_hint text, lifetime_days integer)
RETURNS gregale_auth.service_account_secrets
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE created gregale_auth.service_account_secrets;
BEGIN
  IF lifetime_days IS NOT NULL AND lifetime_days NOT BETWEEN 1 AND 3650 THEN
    RAISE EXCEPTION 'invalid_expires_in_days' USING ERRCODE = '22023';
  END IF;
  PERFORM 1 FROM gregale_auth.service_accounts WHERE id = account FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  IF (SELECT count(*) FROM gregale_auth.service_account_secrets
      WHERE service_account_id = account AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())) >= 2 THEN
    RAISE EXCEPTION 'secret_limit' USING ERRCODE = '54000';
  END IF;
  INSERT INTO gregale_auth.service_account_secrets (service_account_id, secret_hash, hint, created_by, expires_at)
    VALUES (account, hashed_secret, secret_hint, gregale_auth.uid(), now() + make_interval(days => lifetime_days))
    RETURNING * INTO created;
  RETURN created;
END $$;

-- insert_service_account makes an account with its first secret. The
-- organization's lock serializes the count against concurrent creations.
CREATE FUNCTION gregale_auth.insert_service_account(org uuid, account_name text, account_role text, hashed_secret bytea, secret_hint text, lifetime_days integer)
RETURNS TABLE (id uuid, org_id uuid, name text, role text, created_at timestamptz, secret_id uuid, expires_at timestamptz)
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  account gregale_auth.service_accounts;
  secret gregale_auth.service_account_secrets;
BEGIN
  PERFORM 1 FROM gregale_auth.organizations o WHERE o.id = org FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM gregale_auth.roles r WHERE r.name = account_role) THEN
    RAISE EXCEPTION 'unknown_role' USING ERRCODE = '22023';
  END IF;
  IF (SELECT count(*) FROM gregale_auth.service_accounts a WHERE a.org_id = org) >= 50 THEN
    RAISE EXCEPTION 'service_account_limit' USING ERRCODE = '54000';
  END IF;
  INSERT INTO gregale_auth.service_accounts (org_id, name, role, created_by)
    VALUES (org, btrim(account_name), account_role, gregale_auth.uid()) RETURNING * INTO account;
  secret := gregale_auth.add_service_account_secret(account.id, hashed_secret, secret_hint, lifetime_days);
  RETURN QUERY SELECT account.id, account.org_id, account.name, account.role, account.created_at, secret.id, secret.expires_at;
EXCEPTION
  WHEN check_violation THEN RAISE EXCEPTION 'invalid_service_account' USING ERRCODE = '22023';
END $$;

-- change_service_account renames, re-roles, disables or enables an account;
-- a null argument leaves that field as it is.
CREATE FUNCTION gregale_auth.change_service_account(account uuid, new_name text, new_role text, disabled boolean)
RETURNS gregale_auth.service_accounts
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE updated gregale_auth.service_accounts;
BEGIN
  IF new_role IS NOT NULL AND NOT EXISTS (SELECT 1 FROM gregale_auth.roles WHERE name = new_role) THEN
    RAISE EXCEPTION 'unknown_role' USING ERRCODE = '22023';
  END IF;
  UPDATE gregale_auth.service_accounts SET name = coalesce(btrim(new_name), name), role = coalesce(new_role, role),
      disabled_at = CASE WHEN disabled IS NULL THEN disabled_at WHEN disabled THEN coalesce(disabled_at, now()) END
    WHERE id = account RETURNING * INTO updated;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  RETURN updated;
EXCEPTION
  WHEN check_violation THEN RAISE EXCEPTION 'invalid_service_account' USING ERRCODE = '22023';
END $$;

CREATE FUNCTION gregale_auth.revoke_service_account_secret(account uuid, secret uuid) RETURNS void
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  UPDATE gregale_auth.service_account_secrets SET revoked_at = now()
    WHERE id = secret AND service_account_id = account AND revoked_at IS NULL;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
END $$;

-- service_account_credentials is what the token endpoint checks a secret
-- against: an enabled account's live secrets, its organization and role.
CREATE FUNCTION gregale_auth.service_account_credentials(account uuid)
RETURNS TABLE (secret_id uuid, secret_hash bytea, name text, org_id uuid, org_slug text, role text, permissions text[])
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT k.id, k.secret_hash, a.name, o.id, o.slug, a.role, r.permissions
  FROM gregale_auth.service_accounts a
    JOIN gregale_auth.organizations o ON o.id = a.org_id
    JOIN gregale_auth.roles r ON r.name = a.role
    JOIN gregale_auth.service_account_secrets k ON k.service_account_id = a.id
  WHERE a.id = account AND a.disabled_at IS NULL AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at > now())
$$;

-- The organization API: members holding members:manage manage their
-- organization's service accounts from a session. As with invitations,
-- only an owner creates or manages an owner-role account; its secrets are
-- an owner's powers.
CREATE FUNCTION gregale_auth.lock_service_account(org uuid, account uuid) RETURNS gregale_auth.service_accounts
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE existing gregale_auth.service_accounts;
BEGIN
  PERFORM gregale_auth.require_session();
  PERFORM gregale_auth.lock_organization(org, 'members:manage');
  SELECT * INTO existing FROM gregale_auth.service_accounts WHERE id = account AND org_id = org FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  IF existing.role = 'owner' AND gregale_auth.caller_role(org) IS DISTINCT FROM 'owner' THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  RETURN existing;
END $$;

CREATE FUNCTION gregale_auth.list_service_accounts(org uuid)
RETURNS TABLE (id uuid, name text, role text, disabled boolean, created_at timestamptz, secrets jsonb)
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_session();
  IF NOT gregale_auth.has_permission(org, 'members:manage') THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT s.id, s.name, s.role, s.disabled, s.created_at, s.secrets FROM gregale_auth.service_account_rows(org, 50) s;
END $$;

CREATE FUNCTION gregale_auth.create_service_account(org uuid, account_name text, account_role text, hashed_secret bytea, secret_hint text, lifetime_days integer)
RETURNS TABLE (id uuid, org_id uuid, name text, role text, created_at timestamptz, secret_id uuid, expires_at timestamptz)
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_session();
  PERFORM gregale_auth.lock_organization(org, 'members:manage');
  IF account_role = 'owner' AND gregale_auth.caller_role(org) IS DISTINCT FROM 'owner' THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT * FROM gregale_auth.insert_service_account(org, account_name, account_role, hashed_secret, secret_hint, lifetime_days);
END $$;

CREATE FUNCTION gregale_auth.update_service_account(org uuid, account uuid, new_name text, new_role text, disabled boolean)
RETURNS gregale_auth.service_accounts
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.lock_service_account(org, account);
  IF new_role = 'owner' AND gregale_auth.caller_role(org) IS DISTINCT FROM 'owner' THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  RETURN gregale_auth.change_service_account(account, new_name, new_role, disabled);
END $$;

CREATE FUNCTION gregale_auth.delete_service_account(org uuid, account uuid) RETURNS void
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.lock_service_account(org, account);
  DELETE FROM gregale_auth.service_accounts WHERE id = account;
END $$;

CREATE FUNCTION gregale_auth.create_service_account_secret(org uuid, account uuid, hashed_secret bytea, secret_hint text, lifetime_days integer)
RETURNS gregale_auth.service_account_secrets
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.lock_service_account(org, account);
  RETURN gregale_auth.add_service_account_secret(account, hashed_secret, secret_hint, lifetime_days);
END $$;

CREATE FUNCTION gregale_auth.delete_service_account_secret(org uuid, account uuid, secret uuid) RETURNS void
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.lock_service_account(org, account);
  PERFORM gregale_auth.revoke_service_account_secret(account, secret);
END $$;

-- The organization's audit log records each change, with the acting member
-- when it came through the organization API and none for the operator.
ALTER TABLE gregale_auth.org_audit_log DROP CONSTRAINT org_audit_log_action_check,
  ADD CONSTRAINT org_audit_log_action_check CHECK (action IN ('organization.created', 'member.added', 'member.role_changed', 'member.removed',
    'invitation.created', 'invitation.revoked', 'invitation.accepted', 'domain.added', 'domain.verified', 'domain.removed',
    'sso.connected', 'sso.updated', 'sso.disconnected', 'service_account.created', 'service_account.updated', 'service_account.deleted',
    'service_account.secret_created', 'service_account.secret_revoked')) NOT VALID;

CREATE FUNCTION gregale_auth.audit_service_account_change() RETURNS trigger
LANGUAGE plpgsql SET search_path = '' AS $$
DECLARE org uuid;
BEGIN
  IF TG_TABLE_NAME = 'service_accounts' THEN
    IF TG_OP = 'INSERT' THEN
      PERFORM gregale_auth.record_org_event(NEW.org_id, 'service_account.created', NULL, jsonb_build_object('service_account_id', NEW.id, 'name', NEW.name, 'role', NEW.role));
    ELSIF TG_OP = 'UPDATE' THEN
      PERFORM gregale_auth.record_org_event(NEW.org_id, 'service_account.updated', NULL, jsonb_build_object('service_account_id', NEW.id, 'name', NEW.name,
        'from_role', OLD.role, 'to_role', NEW.role, 'disabled', NEW.disabled_at IS NOT NULL));
    ELSE
      PERFORM gregale_auth.record_org_event(OLD.org_id, 'service_account.deleted', NULL, jsonb_build_object('service_account_id', OLD.id, 'name', OLD.name, 'role', OLD.role));
    END IF;
  ELSE
    SELECT a.org_id INTO org FROM gregale_auth.service_accounts a WHERE a.id = NEW.service_account_id;
    IF TG_OP = 'INSERT' THEN
      PERFORM gregale_auth.record_org_event(org, 'service_account.secret_created', NULL, jsonb_build_object('service_account_id', NEW.service_account_id,
        'secret_id', NEW.id, 'hint', NEW.hint, 'expires_at', NEW.expires_at));
    ELSE
      PERFORM gregale_auth.record_org_event(org, 'service_account.secret_revoked', NULL, jsonb_build_object('service_account_id', NEW.service_account_id,
        'secret_id', NEW.id, 'hint', NEW.hint));
    END IF;
  END IF;
  RETURN NULL;
END $$;

CREATE TRIGGER gregale_auth_audit_service_accounts AFTER INSERT OR DELETE ON gregale_auth.service_accounts
  FOR EACH ROW EXECUTE FUNCTION gregale_auth.audit_service_account_change();
CREATE TRIGGER gregale_auth_audit_service_account_changes AFTER UPDATE OF name, role, disabled_at ON gregale_auth.service_accounts
  FOR EACH ROW WHEN (OLD.name IS DISTINCT FROM NEW.name OR OLD.role IS DISTINCT FROM NEW.role OR (OLD.disabled_at IS NULL) <> (NEW.disabled_at IS NULL))
  EXECUTE FUNCTION gregale_auth.audit_service_account_change();
CREATE TRIGGER gregale_auth_audit_service_account_secrets AFTER INSERT ON gregale_auth.service_account_secrets
  FOR EACH ROW EXECUTE FUNCTION gregale_auth.audit_service_account_change();
CREATE TRIGGER gregale_auth_audit_service_account_revocations AFTER UPDATE OF revoked_at ON gregale_auth.service_account_secrets
  FOR EACH ROW WHEN (OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL) EXECUTE FUNCTION gregale_auth.audit_service_account_change();

REVOKE ALL ON FUNCTION gregale_auth.service_account_rows(uuid, integer), gregale_auth.add_service_account_secret(uuid, bytea, text, integer),
  gregale_auth.insert_service_account(uuid, text, text, bytea, text, integer), gregale_auth.change_service_account(uuid, text, text, boolean),
  gregale_auth.revoke_service_account_secret(uuid, uuid), gregale_auth.service_account_credentials(uuid), gregale_auth.lock_service_account(uuid, uuid),
  gregale_auth.list_service_accounts(uuid), gregale_auth.create_service_account(uuid, text, text, bytea, text, integer),
  gregale_auth.update_service_account(uuid, uuid, text, text, boolean), gregale_auth.delete_service_account(uuid, uuid),
  gregale_auth.create_service_account_secret(uuid, uuid, bytea, text, integer), gregale_auth.delete_service_account_secret(uuid, uuid, uuid),
  gregale_auth.audit_service_account_change() FROM PUBLIC;
