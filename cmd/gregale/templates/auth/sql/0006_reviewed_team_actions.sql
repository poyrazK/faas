-- ADR-1600: Auth owns membership mutations, their idempotency and audit receipt.
-- Existing roles, memberships, invitations and acceptance remain authoritative.
CREATE TABLE gregale_auth.team_actions (
  id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
  org_id uuid NOT NULL REFERENCES gregale_auth.organizations(id) ON DELETE CASCADE,
  actor_id uuid NOT NULL CHECK (actor_id <> '00000000-0000-0000-0000-000000000000'),
  actor_role text NOT NULL CHECK (actor_role ~ '^[a-z][a-z0-9_-]{0,31}$'),
  action text NOT NULL CHECK (action IN ('invite','set_role','remove_member','revoke_invitation')),
  fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
  request jsonb NOT NULL CHECK (jsonb_typeof(request) = 'object'),
  result jsonb NOT NULL CHECK (jsonb_typeof(result) = 'object'),
  created_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at))
);
CREATE INDEX team_actions_history ON gregale_auth.team_actions(org_id,created_at DESC,id DESC);
REVOKE ALL ON gregale_auth.team_actions FROM PUBLIC;

CREATE FUNCTION gregale_auth.team_state(org uuid, max_members integer, max_invitations integer) RETURNS jsonb
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE caller uuid := gregale_auth.require_session();
BEGIN
  PERFORM gregale_auth.lock_organization(org, 'members:manage');
  -- A role definition cannot change between review verification and commit.
  -- Auth configuration never takes organization locks, avoiding lock inversion.
  LOCK TABLE gregale_auth.roles IN SHARE MODE;
  -- Configuration may have committed while this transaction waited for SHARE.
  IF NOT gregale_auth.has_permission(org,'members:manage') OR NOT gregale_auth.has_permission(org,'org:read') THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE='42501';
  END IF;
  IF max_members < 1 OR max_invitations < 1 OR
     (SELECT count(*) FROM gregale_auth.memberships WHERE org_id=org) > max_members OR
     (SELECT count(*) FROM gregale_auth.invitations WHERE org_id=org AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at>now()) > max_invitations THEN
    RAISE EXCEPTION 'team_limit' USING ERRCODE = '54000';
  END IF;
  RETURN jsonb_build_object(
    'actor_id',caller,'actor_role',gregale_auth.caller_role(org),
    'members',coalesce((SELECT jsonb_agg(jsonb_build_object('user_id',m.user_id,'email',u.email,'role',m.role,'created_at',m.created_at) ORDER BY m.created_at,m.user_id)
      FROM gregale_auth.memberships m JOIN auth.users u ON u.id=m.user_id WHERE m.org_id=org),'[]'::jsonb),
    'invitations',coalesce((SELECT jsonb_agg(jsonb_build_object('id',i.id,'email',i.email,'role',i.role,'expires_at',i.expires_at,'created_at',i.created_at) ORDER BY i.created_at,i.id)
      FROM gregale_auth.invitations i WHERE i.org_id=org AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at>now()),'[]'::jsonb),
    'roles',coalesce((SELECT jsonb_agg(jsonb_build_object('name',name,'permissions',permissions) ORDER BY name) FROM gregale_auth.roles),'[]'::jsonb));
END $$;

CREATE FUNCTION gregale_auth.team_read_action(org uuid, action_id uuid) RETURNS jsonb
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_session();
  IF NOT gregale_auth.has_permission(org,'members:manage') OR NOT gregale_auth.has_permission(org,'org:read') THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE='42501';
  END IF;
  RETURN (SELECT to_jsonb(a) FROM gregale_auth.team_actions a WHERE a.org_id=org AND a.id=action_id);
END $$;

CREATE FUNCTION gregale_auth.team_record_action(org uuid, action_id uuid, actor_role_before text, kind text, request_hash text, request_body jsonb, action_result jsonb) RETURNS jsonb
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
DECLARE stored gregale_auth.team_actions;
BEGIN
  -- Only the Auth front may invoke this function, after the authorized mutation
  -- in this same transaction. Self-demotion/removal must still retain its audit.
  INSERT INTO gregale_auth.team_actions(id,org_id,actor_id,actor_role,action,fingerprint,request,result)
    VALUES(action_id,org,gregale_auth.require_session(),actor_role_before,kind,request_hash,request_body,action_result)
    RETURNING * INTO stored;
  RETURN to_jsonb(stored);
END $$;

CREATE FUNCTION gregale_auth.team_list_actions(org uuid, before_at timestamptz, before_id uuid, page_limit integer) RETURNS SETOF jsonb
LANGUAGE plpgsql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  PERFORM gregale_auth.require_session();
  IF NOT gregale_auth.has_permission(org,'members:manage') OR NOT gregale_auth.has_permission(org,'org:read') THEN
    RAISE EXCEPTION 'forbidden' USING ERRCODE='42501';
  END IF;
  IF page_limit < 1 THEN RAISE EXCEPTION 'invalid_request' USING ERRCODE='22023'; END IF;
  RETURN QUERY SELECT to_jsonb(a)-'fingerprint' FROM gregale_auth.team_actions a
    WHERE a.org_id=org AND (before_at IS NULL OR (a.created_at,a.id)<(before_at,before_id))
    ORDER BY a.created_at DESC,a.id DESC LIMIT page_limit;
END $$;
REVOKE ALL ON FUNCTION gregale_auth.team_state(uuid,integer,integer),gregale_auth.team_read_action(uuid,uuid),
  gregale_auth.team_record_action(uuid,uuid,text,text,text,jsonb,jsonb),gregale_auth.team_list_actions(uuid,timestamptz,uuid,integer) FROM PUBLIC;
