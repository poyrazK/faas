-- Role grants and invitation limits (ADR-1190). Members could give any role
-- but owner, themselves included, so a member holding members:manage could
-- take a custom role carrying permissions it lacked. From here on a member
-- gives, changes or removes only roles whose every permission it holds
-- (can_grant): to members, invitations, service accounts, people joining by
-- email domain and identity-provider users. Invitations, which email any
-- address, were also unthrottled; an inviter sends at most 50 an hour, an
-- address receives at most 10 a day, and an address with a pending
-- invitation to the organization gets no second one. The functions keep their
-- signatures; the new indexes are on the small invitations table.
SET LOCAL lock_timeout = '5s';

CREATE INDEX invitations_inviter_idx ON gregale_auth.invitations (invited_by, created_at);
CREATE INDEX invitations_email_idx ON gregale_auth.invitations (email, created_at);

-- can_grant is true when the caller holds every permission of the role in
-- the organization. '*' covers every permission, and a role holding '*'
-- needs a caller who holds '*' too.
CREATE FUNCTION gregale_auth.can_grant(org uuid, granted_role text) RETURNS boolean
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT EXISTS (
    SELECT 1 FROM gregale_auth.memberships m
      JOIN gregale_auth.roles mine ON mine.name = m.role
      JOIN gregale_auth.roles r ON r.name = granted_role
    WHERE m.org_id = org AND m.user_id = gregale_auth.uid()
      AND ('*' = ANY (mine.permissions) OR r.permissions <@ mine.permissions))
$$;

CREATE OR REPLACE FUNCTION gregale_auth.invite(org uuid, invitee_email text, invitee_role text, hashed_token bytea, ttl_seconds integer)
RETURNS gregale_auth.invitations
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  caller uuid := gregale_auth.require_session();
  address text := lower(btrim(invitee_email));
  created gregale_auth.invitations;
BEGIN
  PERFORM gregale_auth.lock_organization(org, 'members:manage');
  IF NOT EXISTS (SELECT 1 FROM gregale_auth.roles WHERE name = invitee_role) THEN
    RAISE EXCEPTION 'unknown_role' USING ERRCODE = '22023';
  END IF;
  IF (invitee_role = 'owner' AND gregale_auth.caller_role(org) IS DISTINCT FROM 'owner') OR NOT gregale_auth.can_grant(org, invitee_role) THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  IF ttl_seconds NOT BETWEEN 300 AND 1209600 THEN
    RAISE EXCEPTION 'invalid_invitation' USING ERRCODE = '22023';
  END IF;
  IF EXISTS (SELECT 1 FROM gregale_auth.invitations
      WHERE org_id = org AND email = address AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()) THEN
    RAISE EXCEPTION 'already_invited' USING ERRCODE = '23505';
  END IF;
  IF (SELECT count(*) FROM gregale_auth.invitations
      WHERE org_id = org AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()) >= 500 THEN
    RAISE EXCEPTION 'invitation_limit' USING ERRCODE = '54000';
  END IF;
  IF (SELECT count(*) FROM gregale_auth.invitations WHERE invited_by = caller AND created_at > now() - interval '1 hour') >= 50
     OR (SELECT count(*) FROM gregale_auth.invitations WHERE email = address AND created_at > now() - interval '1 day') >= 10 THEN
    RAISE EXCEPTION 'invitation_rate_limited' USING ERRCODE = '53400';
  END IF;
  INSERT INTO gregale_auth.invitations (org_id, email, role, token_hash, invited_by, expires_at)
    VALUES (org, address, invitee_role, hashed_token, caller, now() + make_interval(secs => ttl_seconds))
    RETURNING * INTO created;
  RETURN created;
EXCEPTION
  WHEN check_violation THEN RAISE EXCEPTION 'invalid_invitation' USING ERRCODE = '22023';
END $$;

CREATE OR REPLACE FUNCTION gregale_auth.set_member_role(org uuid, member uuid, new_role text) RETURNS gregale_auth.memberships
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
  IF ((existing.role = 'owner' OR new_role = 'owner') AND gregale_auth.caller_role(org) IS DISTINCT FROM 'owner')
     OR NOT gregale_auth.can_grant(org, existing.role) OR NOT gregale_auth.can_grant(org, new_role) THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  IF existing.role = 'owner' AND new_role <> 'owner'
     AND (SELECT count(*) FROM gregale_auth.memberships WHERE org_id = org AND role = 'owner') = 1 THEN
    RAISE EXCEPTION 'last_owner' USING ERRCODE = '23514';
  END IF;
  UPDATE gregale_auth.memberships SET role = new_role WHERE org_id = org AND user_id = member RETURNING * INTO updated;
  RETURN updated;
END $$;

-- Members may always leave; removing someone else needs members:manage and
-- every permission of their role, and only owners remove owners. No
-- organization may lose its last owner.
CREATE OR REPLACE FUNCTION gregale_auth.remove_member(org uuid, member uuid) RETURNS void
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
  IF member <> caller AND ((existing.role = 'owner' AND gregale_auth.caller_role(org) IS DISTINCT FROM 'owner') OR NOT gregale_auth.can_grant(org, existing.role)) THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  IF existing.role = 'owner' AND (SELECT count(*) FROM gregale_auth.memberships WHERE org_id = org AND role = 'owner') = 1 THEN
    RAISE EXCEPTION 'last_owner' USING ERRCODE = '23514';
  END IF;
  DELETE FROM gregale_auth.memberships WHERE org_id = org AND user_id = member;
END $$;

-- Service accounts (sql/0021): managing one needs every permission of its
-- role, and giving one a role needs every permission of that role.
CREATE OR REPLACE FUNCTION gregale_auth.lock_service_account(org uuid, account uuid) RETURNS gregale_auth.service_accounts
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE existing gregale_auth.service_accounts;
BEGIN
  PERFORM gregale_auth.require_session();
  PERFORM gregale_auth.lock_organization(org, 'members:manage');
  SELECT * INTO existing FROM gregale_auth.service_accounts WHERE id = account AND org_id = org FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  IF (existing.role = 'owner' AND gregale_auth.caller_role(org) IS DISTINCT FROM 'owner') OR NOT gregale_auth.can_grant(org, existing.role) THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  RETURN existing;
END $$;

CREATE OR REPLACE FUNCTION gregale_auth.create_service_account(org uuid, account_name text, account_role text, hashed_secret bytea, secret_hint text, lifetime_days integer)
RETURNS TABLE (id uuid, org_id uuid, name text, role text, created_at timestamptz, secret_id uuid, expires_at timestamptz)
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_session();
  PERFORM gregale_auth.lock_organization(org, 'members:manage');
  IF (account_role = 'owner' AND gregale_auth.caller_role(org) IS DISTINCT FROM 'owner')
     OR (EXISTS (SELECT 1 FROM gregale_auth.roles r WHERE r.name = account_role) AND NOT gregale_auth.can_grant(org, account_role)) THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT * FROM gregale_auth.insert_service_account(org, account_name, account_role, hashed_secret, secret_hint, lifetime_days);
END $$;

CREATE OR REPLACE FUNCTION gregale_auth.update_service_account(org uuid, account uuid, new_name text, new_role text, disabled boolean)
RETURNS gregale_auth.service_accounts
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.lock_service_account(org, account);
  IF new_role IS NOT NULL AND ((new_role = 'owner' AND gregale_auth.caller_role(org) IS DISTINCT FROM 'owner')
     OR (EXISTS (SELECT 1 FROM gregale_auth.roles r WHERE r.name = new_role) AND NOT gregale_auth.can_grant(org, new_role))) THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  RETURN gregale_auth.change_service_account(account, new_name, new_role, disabled);
END $$;

-- People joining by a verified email domain (sql/0017) get the domain's
-- role, so setting it needs every permission of that role.
CREATE OR REPLACE FUNCTION gregale_auth.set_domain_join(org uuid, requested text, mode text, member_role text)
RETURNS TABLE (domain text, join_mode text, join_role text, joined integer)
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  existing gregale_auth.org_domains;
  added integer := 0;
BEGIN
  PERFORM gregale_auth.require_domain_manager(org);
  PERFORM 1 FROM gregale_auth.organizations o WHERE o.id = org FOR UPDATE;
  IF mode IS NULL OR mode NOT IN ('off', 'suggest', 'automatic') THEN
    RAISE EXCEPTION 'invalid_join_mode' USING ERRCODE = '22023';
  END IF;
  IF member_role IS NULL OR member_role = 'owner' OR NOT EXISTS (SELECT 1 FROM gregale_auth.roles r WHERE r.name = member_role) THEN
    RAISE EXCEPTION 'unknown_role' USING ERRCODE = '22023';
  END IF;
  IF NOT gregale_auth.can_grant(org, member_role) THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
  SELECT * INTO existing FROM gregale_auth.org_domains d WHERE d.org_id = org AND d.domain = lower(requested) FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  IF existing.verified_at IS NULL AND mode <> 'off' THEN
    RAISE EXCEPTION 'domain_not_verified' USING ERRCODE = '42501';
  END IF;
  UPDATE gregale_auth.org_domains d SET join_mode = mode, join_role = member_role WHERE d.org_id = org AND d.domain = existing.domain;
  IF mode = 'automatic' THEN
    INSERT INTO gregale_auth.memberships (org_id, user_id, role)
    SELECT org, u.id, member_role FROM auth.users u
    WHERE lower(u.email) LIKE '%@' || existing.domain AND gregale_auth.proven_email_domain(u.id) = existing.domain
      AND NOT EXISTS (SELECT 1 FROM gregale_auth.membership_history h WHERE h.org_id = org AND h.user_id = u.id)
    ON CONFLICT (org_id, user_id) DO NOTHING;
    GET DIAGNOSTICS added = ROW_COUNT;
  END IF;
  RETURN QUERY SELECT existing.domain, mode, member_role, added;
END $$;

-- An organization's identity provider gives its people the linked role
-- (sql/0007), so linking needs every permission of that role.
CREATE OR REPLACE FUNCTION gregale_auth.check_org_sso(org uuid, provider uuid, domains text[], member_role text) RETURNS void
LANGUAGE plpgsql STABLE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_sso_manager(org);
  IF provider IS NOT NULL AND NOT EXISTS (SELECT 1 FROM gregale_auth.sso_organizations WHERE provider_id = provider AND org_id = org) THEN
    RAISE EXCEPTION 'not_found' USING ERRCODE = 'P0002';
  END IF;
  IF provider IS NULL AND (SELECT count(*) FROM gregale_auth.sso_organizations WHERE org_id = org) >= 3 THEN
    RAISE EXCEPTION 'sso_provider_limit' USING ERRCODE = '54000';
  END IF;
  IF domains IS NOT NULL AND EXISTS (SELECT 1 FROM unnest(domains) AS requested(domain)
      WHERE NOT EXISTS (SELECT 1 FROM gregale_auth.org_domains d WHERE d.org_id = org AND d.domain = lower(requested.domain) AND d.verified_at IS NOT NULL)) THEN
    RAISE EXCEPTION 'domain_not_verified' USING ERRCODE = '42501';
  END IF;
  IF member_role IS NOT NULL AND (member_role = 'owner' OR NOT EXISTS (SELECT 1 FROM gregale_auth.roles WHERE name = member_role)) THEN
    RAISE EXCEPTION 'unknown_role' USING ERRCODE = '22023';
  END IF;
  IF member_role IS NOT NULL AND NOT gregale_auth.can_grant(org, member_role) THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE = '42501';
  END IF;
END $$;

REVOKE ALL ON FUNCTION gregale_auth.can_grant(uuid, text) FROM PUBLIC;
