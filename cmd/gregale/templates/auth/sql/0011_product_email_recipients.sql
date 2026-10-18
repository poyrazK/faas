-- ADR-1720. A recipient capability can only resolve one consenting user's
-- verified address and current membership for one product and organization.
CREATE TABLE gregale_auth.product_email_recipients (
 org_id uuid NOT NULL REFERENCES gregale_auth.organizations(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
 product_id uuid NOT NULL CHECK(product_id <> '00000000-0000-0000-0000-000000000000'),
 token_hash bytea NOT NULL CHECK(octet_length(token_hash)=32),
 email_at_consent text NOT NULL CHECK(octet_length(email_at_consent) BETWEEN 3 AND 320),
 expires_at timestamptz NOT NULL CHECK(isfinite(expires_at)),
 PRIMARY KEY(org_id,user_id,product_id),
 FOREIGN KEY(org_id,user_id) REFERENCES gregale_auth.memberships(org_id,user_id) ON DELETE CASCADE,
 UNIQUE(token_hash)
);
REVOKE ALL ON gregale_auth.product_email_recipients FROM PUBLIC;

CREATE FUNCTION gregale_auth.product_email_self(org uuid)
RETURNS TABLE(actor_id uuid, organization_id uuid, role text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF NOT gregale_auth.session_active() THEN RAISE EXCEPTION 'not_authenticated' USING ERRCODE='28000'; END IF;
 RETURN QUERY SELECT m.user_id,m.org_id,m.role FROM gregale_auth.memberships m
 JOIN auth.users u ON u.id=m.user_id WHERE m.org_id=org AND m.user_id=gregale_auth.uid()
 AND u.email_confirmed_at IS NOT NULL AND u.email IS NOT NULL AND NOT coalesce(u.is_anonymous,false);
 IF NOT FOUND THEN RAISE EXCEPTION 'forbidden' USING ERRCODE='42501'; END IF;
END $$;

CREATE FUNCTION gregale_auth.product_email_consent(org uuid, product uuid, hashed bytea, lifetime integer)
RETURNS TABLE(actor_id uuid, organization_id uuid, role text, expires_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE self record; expiry timestamptz := now()+make_interval(secs=>lifetime);
BEGIN
 SELECT * INTO self FROM gregale_auth.product_email_self(org);
 PERFORM 1 FROM auth.users WHERE id=self.actor_id FOR UPDATE;
 DELETE FROM gregale_auth.product_email_recipients r WHERE r.user_id=self.actor_id AND r.expires_at<=now();
 IF NOT EXISTS(SELECT 1 FROM gregale_auth.product_email_recipients r WHERE r.org_id=org AND r.user_id=self.actor_id AND r.product_id=product)
 AND (SELECT count(*) FROM gregale_auth.product_email_recipients r WHERE r.user_id=self.actor_id)>=100 THEN
 RAISE EXCEPTION 'notification_recipient_limit' USING ERRCODE='54000'; END IF;
 IF product='00000000-0000-0000-0000-000000000000' OR octet_length(hashed)<>32 OR lifetime<>2592000 THEN
 RAISE EXCEPTION 'invalid_request' USING ERRCODE='22023'; END IF;
 INSERT INTO gregale_auth.product_email_recipients AS r(org_id,user_id,product_id,token_hash,email_at_consent,expires_at)
 SELECT org,self.actor_id,product,hashed,u.email,expiry FROM auth.users u WHERE u.id=self.actor_id
 ON CONFLICT(org_id,user_id,product_id) DO UPDATE SET token_hash=excluded.token_hash,email_at_consent=excluded.email_at_consent,expires_at=excluded.expires_at;
 RETURN QUERY SELECT self.actor_id,self.organization_id,self.role,expiry;
END $$;

CREATE FUNCTION gregale_auth.product_email_recipient(org uuid, product uuid, hashed bytea)
RETURNS TABLE(actor_id uuid, organization_id uuid, role text, email text, expires_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT r.user_id,r.org_id,m.role,u.email::text,r.expires_at
 FROM gregale_auth.product_email_recipients r
 JOIN gregale_auth.memberships m ON m.user_id=r.user_id AND m.org_id=r.org_id
 JOIN auth.users u ON u.id=r.user_id
 WHERE r.org_id=org AND r.product_id=product AND r.token_hash=hashed AND r.expires_at>now()
 AND u.email=r.email_at_consent AND u.email_confirmed_at IS NOT NULL
 AND u.deleted_at IS NULL AND (u.banned_until IS NULL OR u.banned_until<=now())
 AND NOT coalesce(u.is_anonymous,false)
$$;
REVOKE ALL ON FUNCTION gregale_auth.product_email_self(uuid), gregale_auth.product_email_consent(uuid,uuid,bytea,integer), gregale_auth.product_email_recipient(uuid,uuid,bytea) FROM PUBLIC;
