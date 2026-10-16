-- Bearer invitations: only a SHA-256 digest is persisted. Raw secrets are never receipts.
CREATE TABLE api.team_invitations (
  invitation_id uuid PRIMARY KEY,
  tenant_id text NOT NULL,
  issuer text NOT NULL,
  invited_role api.team_role NOT NULL CHECK (invited_role IN ('member','admin')),
  secret_hash bytea NOT NULL CHECK (octet_length(secret_hash)=32),
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  revoked_at timestamptz,
  accepted_at timestamptz,
  accepted_subject text,
  accepted_revision uuid,
  CHECK ((accepted_at IS NULL AND accepted_subject IS NULL AND accepted_revision IS NULL)
    OR (accepted_at IS NOT NULL AND accepted_subject IS NOT NULL AND accepted_revision IS NOT NULL)),
  CHECK (revoked_at IS NULL OR accepted_at IS NULL)
);
CREATE INDEX team_invitation_page ON api.team_invitations(tenant_id,invitation_id);
ALTER TABLE api.team_invitations ENABLE ROW LEVEL SECURITY;
CREATE POLICY invitation_reads ON api.team_invitations FOR SELECT USING (
  EXISTS (SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_invitations.tenant_id
    AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner')
  OR invitation_id::text=current_setting('gregale.invitation_scope',true)
);
CREATE POLICY invitation_creates ON api.team_invitations FOR INSERT WITH CHECK (
  invitation_id::text=current_setting('gregale.invitation_scope',true)
  AND issuer=current_setting('request.jwt.claims',true)::jsonb->>'sub'
);
CREATE POLICY invitation_updates ON api.team_invitations FOR UPDATE USING (
  invitation_id::text=current_setting('gregale.invitation_scope',true)
) WITH CHECK (invitation_id::text=current_setting('gregale.invitation_scope',true));
-- Only acceptance can register its verified JWT subject, never a submitted user ID.
CREATE POLICY invitation_user_registration ON api.team_users FOR INSERT WITH CHECK (
  subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
  AND subject=current_setting('gregale.invitation_register',true)
);
CREATE TABLE api.team_invitation_events (
  event_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id text NOT NULL,
  invitation_id uuid NOT NULL REFERENCES api.team_invitations(invitation_id),
  actor text NOT NULL,
  action text NOT NULL CHECK(action IN ('create','revoke','accept')),
  occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(invitation_id,action)
);
ALTER TABLE api.team_invitation_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY invitation_history_reads ON api.team_invitation_events FOR SELECT USING (
  EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_invitation_events.tenant_id
    AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND m.role='owner')
);
CREATE POLICY invitation_history_creates ON api.team_invitation_events FOR INSERT WITH CHECK (
  invitation_id::text=current_setting('gregale.invitation_scope',true)
  AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub'
);

CREATE FUNCTION api.create_team_invitation_once(operation_id uuid,team_id text,invite_secret text,invite_role api.team_role,valid_until timestamptz) RETURNS SETOF api.team_invitations
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text := NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
  result api.team_invitations;
  joined api.team_members;
  old_invitation_scope text := current_setting('gregale.invitation_scope',true);
  old_invitation_register text := current_setting('gregale.invitation_register',true);
  old_team_member_read text := current_setting('gregale.team_member_read',true);
  old_team_member_write text := current_setting('gregale.team_member_write',true);
BEGIN
  IF caller_subject IS NULL OR length(caller_subject)>256 OR team_id IS NULL OR team_id='' OR operation_id IS NULL THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_invitation';
  END IF;
  IF current_setting('transaction_isolation') <> 'read committed' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='membership_read_committed_required';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject AND m.role='owner') THEN
    RAISE SQLSTATE '42501' USING MESSAGE='team_owner_required';
  END IF;
  PERFORM set_config('gregale.invitation_scope',operation_id::text,true);
  IF invite_secret IS NULL OR invite_secret !~ '^[0-9a-f]{64}$' OR invite_role IS NULL OR invite_role NOT IN ('member','admin') OR valid_until IS NULL THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_invitation';
  END IF;
  SELECT i.* INTO result FROM api.team_invitations i WHERE i.invitation_id=operation_id;
  IF FOUND THEN
    IF result.tenant_id<>team_id OR result.issuer<>caller_subject OR result.secret_hash<>sha256(convert_to(invite_secret,'UTF8')) OR result.invited_role<>invite_role OR result.expires_at<>valid_until THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE='invitation_payload_conflict';
    END IF;
  ELSE
    -- Application invitation lifetime: positive and at most seven days.
    IF valid_until<=clock_timestamp() OR valid_until>clock_timestamp()+interval '7 days' THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_invitation_expiry';
    END IF;
    INSERT INTO api.team_invitations(invitation_id,tenant_id,issuer,invited_role,secret_hash,expires_at)
      VALUES(operation_id,team_id,caller_subject,invite_role,sha256(convert_to(invite_secret,'UTF8')),valid_until) RETURNING * INTO result;
    INSERT INTO api.team_invitation_events(tenant_id,invitation_id,actor,action) VALUES(team_id,operation_id,caller_subject,'create');
  END IF;
  PERFORM set_config('gregale.invitation_scope',COALESCE(old_invitation_scope,''),true);
  PERFORM set_config('gregale.invitation_register',COALESCE(old_invitation_register,''),true);
  PERFORM set_config('gregale.team_member_read',COALESCE(old_team_member_read,''),true);
  PERFORM set_config('gregale.team_member_write',COALESCE(old_team_member_write,''),true);
  RETURN NEXT result;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.invitation_scope',COALESCE(old_invitation_scope,''),true);
  PERFORM set_config('gregale.invitation_register',COALESCE(old_invitation_register,''),true);
  PERFORM set_config('gregale.team_member_read',COALESCE(old_team_member_read,''),true);
  PERFORM set_config('gregale.team_member_write',COALESCE(old_team_member_write,''),true);
  RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.create_team_invitation_once(uuid,text,text,api.team_role,timestamptz) FROM PUBLIC;
COMMENT ON FUNCTION api.create_team_invitation_once(uuid,text,text,api.team_role,timestamptz) IS '@gregale:rpc';

CREATE FUNCTION api.revoke_team_invitation_once(operation_id uuid,team_id text) RETURNS SETOF api.team_invitations
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text := NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
  result api.team_invitations;
  joined api.team_members;
  old_invitation_scope text := current_setting('gregale.invitation_scope',true);
  old_invitation_register text := current_setting('gregale.invitation_register',true);
  old_team_member_read text := current_setting('gregale.team_member_read',true);
  old_team_member_write text := current_setting('gregale.team_member_write',true);
BEGIN
  IF caller_subject IS NULL OR length(caller_subject)>256 OR team_id IS NULL OR team_id='' OR operation_id IS NULL THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_invitation';
  END IF;
  IF current_setting('transaction_isolation') <> 'read committed' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='membership_read_committed_required';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject AND m.role='owner') THEN
    RAISE SQLSTATE '42501' USING MESSAGE='team_owner_required';
  END IF;
  PERFORM set_config('gregale.invitation_scope',operation_id::text,true);
  SELECT i.* INTO result FROM api.team_invitations i WHERE i.invitation_id=operation_id AND i.tenant_id=team_id;
  IF NOT FOUND THEN RAISE SQLSTATE 'PT409' USING MESSAGE='invitation_unavailable'; END IF;
  IF result.accepted_at IS NOT NULL THEN RAISE SQLSTATE 'PT409' USING MESSAGE='invitation_already_accepted'; END IF;
  IF result.revoked_at IS NULL THEN
    UPDATE api.team_invitations i SET revoked_at=clock_timestamp() WHERE i.invitation_id=operation_id RETURNING i.* INTO result;
    INSERT INTO api.team_invitation_events(tenant_id,invitation_id,actor,action) VALUES(team_id,operation_id,caller_subject,'revoke');
  END IF;
  PERFORM set_config('gregale.invitation_scope',COALESCE(old_invitation_scope,''),true);
  PERFORM set_config('gregale.invitation_register',COALESCE(old_invitation_register,''),true);
  PERFORM set_config('gregale.team_member_read',COALESCE(old_team_member_read,''),true);
  PERFORM set_config('gregale.team_member_write',COALESCE(old_team_member_write,''),true);
  RETURN NEXT result;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.invitation_scope',COALESCE(old_invitation_scope,''),true);
  PERFORM set_config('gregale.invitation_register',COALESCE(old_invitation_register,''),true);
  PERFORM set_config('gregale.team_member_read',COALESCE(old_team_member_read,''),true);
  PERFORM set_config('gregale.team_member_write',COALESCE(old_team_member_write,''),true);
  RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.revoke_team_invitation_once(uuid,text) FROM PUBLIC;
COMMENT ON FUNCTION api.revoke_team_invitation_once(uuid,text) IS '@gregale:rpc';

CREATE FUNCTION api.accept_team_invitation_once(operation_id uuid,team_id text,invite_secret text) RETURNS SETOF api.team_invitations
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text := NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
  result api.team_invitations;
  joined api.team_members;
  old_invitation_scope text := current_setting('gregale.invitation_scope',true);
  old_invitation_register text := current_setting('gregale.invitation_register',true);
  old_team_member_read text := current_setting('gregale.team_member_read',true);
  old_team_member_write text := current_setting('gregale.team_member_write',true);
BEGIN
  IF caller_subject IS NULL OR length(caller_subject)>256 OR team_id IS NULL OR team_id='' OR operation_id IS NULL THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_invitation';
  END IF;
  IF current_setting('transaction_isolation') <> 'read committed' THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='membership_read_committed_required';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id,0));
  PERFORM set_config('gregale.invitation_scope',operation_id::text,true);
  IF invite_secret IS NULL OR invite_secret !~ '^[0-9a-f]{64}$' THEN RAISE SQLSTATE 'PT409' USING MESSAGE='invitation_unavailable'; END IF;
  SELECT i.* INTO result FROM api.team_invitations i WHERE i.invitation_id=operation_id AND i.tenant_id=team_id AND i.secret_hash=sha256(convert_to(invite_secret,'UTF8'));
  IF NOT FOUND THEN RAISE SQLSTATE 'PT409' USING MESSAGE='invitation_unavailable'; END IF;
  IF result.accepted_at IS NOT NULL THEN
    -- Historical receipt, never re-grant access after a subsequent removal.
    IF result.accepted_subject<>caller_subject THEN RAISE SQLSTATE 'PT409' USING MESSAGE='invitation_unavailable'; END IF;
  ELSE
    PERFORM set_config('gregale.team_member_read',team_id,true);
    IF result.revoked_at IS NOT NULL OR result.expires_at<=clock_timestamp()
      OR NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=result.issuer AND m.role='owner') THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE='invitation_unavailable';
    END IF;
    PERFORM set_config('gregale.team_member_read',team_id,true);
    PERFORM set_config('gregale.team_member_write',team_id,true);
    SELECT m.* INTO joined FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject;
    IF FOUND THEN RAISE SQLSTATE 'PT409' USING MESSAGE='already_team_member'; END IF;
    PERFORM set_config('gregale.invitation_register',caller_subject,true);
    INSERT INTO api.team_users(subject) VALUES(caller_subject) ON CONFLICT(subject) DO NOTHING;
    INSERT INTO api.team_members(tenant_id,subject,role) VALUES(team_id,caller_subject,result.invited_role) RETURNING * INTO joined;
    UPDATE api.team_invitations i SET accepted_at=clock_timestamp(),accepted_subject=caller_subject,accepted_revision=joined.revision WHERE i.invitation_id=operation_id RETURNING i.* INTO result;
    INSERT INTO api.team_invitation_events(tenant_id,invitation_id,actor,action) VALUES(team_id,operation_id,caller_subject,'accept');
  END IF;
  PERFORM set_config('gregale.invitation_scope',COALESCE(old_invitation_scope,''),true);
  PERFORM set_config('gregale.invitation_register',COALESCE(old_invitation_register,''),true);
  PERFORM set_config('gregale.team_member_read',COALESCE(old_team_member_read,''),true);
  PERFORM set_config('gregale.team_member_write',COALESCE(old_team_member_write,''),true);
  RETURN NEXT result;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.invitation_scope',COALESCE(old_invitation_scope,''),true);
  PERFORM set_config('gregale.invitation_register',COALESCE(old_invitation_register,''),true);
  PERFORM set_config('gregale.team_member_read',COALESCE(old_team_member_read,''),true);
  PERFORM set_config('gregale.team_member_write',COALESCE(old_team_member_write,''),true);
  RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.accept_team_invitation_once(uuid,text,text) FROM PUBLIC;
COMMENT ON FUNCTION api.accept_team_invitation_once(uuid,text,text) IS '@gregale:rpc';

-- Allows a not-yet-member client to bind recovery to its verified actor.
CREATE FUNCTION api.team_invitation_identity() RETURNS SETOF api.team_users
LANGUAGE sql STABLE SECURITY INVOKER SET search_path=pg_catalog AS $$
  SELECT ROW(NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub',''))::api.team_users;
$$;
REVOKE EXECUTE ON FUNCTION api.team_invitation_identity() FROM PUBLIC;
COMMENT ON FUNCTION api.team_invitation_identity() IS '@gregale:rpc';
