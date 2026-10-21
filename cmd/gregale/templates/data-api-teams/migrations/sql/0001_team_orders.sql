CREATE SCHEMA IF NOT EXISTS api;
CREATE TYPE api.order_status AS ENUM ('draft', 'submitted', 'fulfilled');
CREATE TYPE api.team_role AS ENUM ('owner', 'admin', 'member');
CREATE TABLE api.team_members (
  tenant_id text NOT NULL,
  subject text NOT NULL,
  role api.team_role NOT NULL,
  PRIMARY KEY (tenant_id, subject)
);
ALTER TABLE api.team_members ENABLE ROW LEVEL SECURITY;
-- Membership is provisioned by the migration owner, never by application JWTs.
CREATE POLICY own_memberships ON api.team_members FOR SELECT
  USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub');

CREATE TABLE api.team_orders (
  id uuid NOT NULL,
  tenant_id text NOT NULL,
  created_by text NOT NULL,
  total_cents integer NOT NULL CHECK (total_cents >= 0),
  status api.order_status NOT NULL DEFAULT 'draft',
  version integer NOT NULL DEFAULT 1 CHECK (version > 0),
  PRIMARY KEY (tenant_id, id)
);
CREATE INDEX team_orders_status_page ON api.team_orders (tenant_id, status, id);
ALTER TABLE api.team_orders ENABLE ROW LEVEL SECURITY;
CREATE POLICY team_reads ON api.team_orders FOR SELECT USING (
  EXISTS (SELECT 1 FROM api.team_members m WHERE m.tenant_id = team_orders.tenant_id)
);
-- Only the reviewed RPC can enter this transaction-local scope over HTTP.
CREATE POLICY team_creates ON api.team_orders FOR INSERT WITH CHECK (
  current_setting('gregale.team_order_write', true) = 'on'
  AND EXISTS (SELECT 1 FROM api.team_members m WHERE m.tenant_id = team_orders.tenant_id AND m.role IN ('owner', 'admin'))
);
CREATE POLICY team_updates ON api.team_orders FOR UPDATE USING (
  current_setting('gregale.team_order_write', true) = 'on'
  AND EXISTS (SELECT 1 FROM api.team_members m WHERE m.tenant_id = team_orders.tenant_id AND m.role IN ('owner', 'admin'))
) WITH CHECK (
  current_setting('gregale.team_order_write', true) = 'on'
  AND EXISTS (SELECT 1 FROM api.team_members m WHERE m.tenant_id = team_orders.tenant_id AND m.role IN ('owner', 'admin'))
);

CREATE TABLE api.team_order_receipts (
  tenant_id text NOT NULL,
  subject text NOT NULL,
  request_key uuid NOT NULL,
  payload jsonb NOT NULL,
  response jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, subject, request_key)
);
ALTER TABLE api.team_order_receipts ENABLE ROW LEVEL SECURITY;
CREATE POLICY scoped_receipt_reads ON api.team_order_receipts FOR SELECT USING (
  current_setting('gregale.team_order_write', true) = 'on'
  AND subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
  AND EXISTS (SELECT 1 FROM api.team_members m WHERE m.tenant_id = team_order_receipts.tenant_id AND m.role IN ('owner', 'admin'))
);
CREATE POLICY scoped_receipt_inserts ON api.team_order_receipts FOR INSERT WITH CHECK (
  current_setting('gregale.team_order_write', true) = 'on'
  AND subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
  AND EXISTS (SELECT 1 FROM api.team_members m WHERE m.tenant_id = team_order_receipts.tenant_id AND m.role IN ('owner', 'admin'))
);

-- expected_version=0 creates the caller's chosen UUID; positive values update
-- exactly that version. The response receipt is immutable despite later edits.
CREATE FUNCTION api.save_team_order_once(request_key uuid, team_id text,
  order_id uuid, expected_version integer, order_total integer,
  order_state api.order_status)
RETURNS SETOF api.team_orders LANGUAGE plpgsql SECURITY INVOKER
SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  actor text := NULLIF(current_setting('request.jwt.claims', true)::jsonb ->> 'sub', '');
  previous_scope text := current_setting('gregale.team_order_write', true);
  intended jsonb;
  saved api.team_order_receipts;
  result api.team_orders;
BEGIN
  IF actor IS NULL OR request_key IS NULL OR team_id IS NULL OR team_id = ''
    OR order_id IS NULL OR expected_version IS NULL OR expected_version < 0
    OR order_total IS NULL OR order_total < 0 OR order_state IS NULL THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'invalid_order';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM api.team_members m WHERE m.tenant_id = team_id AND m.role IN ('owner', 'admin')) THEN
    RAISE SQLSTATE '42501' USING MESSAGE = 'team_write_denied';
  END IF;
  intended := jsonb_build_object('id', order_id, 'version', expected_version,
    'total', order_total, 'status', order_state);
  PERFORM pg_advisory_xact_lock(hashtextextended(actor || ':' || team_id || ':' || request_key::text, 0));
  PERFORM set_config('gregale.team_order_write', 'on', true);
  SELECT r.* INTO saved FROM api.team_order_receipts r
    WHERE r.tenant_id = team_id AND r.subject = actor AND r.request_key = save_team_order_once.request_key;
  IF FOUND THEN
    IF saved.payload <> intended THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict';
    END IF;
    SELECT * INTO result FROM jsonb_populate_record(NULL::api.team_orders, saved.response);
  ELSE
    IF expected_version = 0 THEN
      INSERT INTO api.team_orders(id, tenant_id, created_by, total_cents, status)
        VALUES(order_id, team_id, actor, order_total, order_state) RETURNING * INTO result;
    ELSE
      UPDATE api.team_orders o SET total_cents = order_total, status = order_state, version = o.version + 1
        WHERE o.id = order_id AND o.tenant_id = team_id AND o.version = expected_version RETURNING o.* INTO result;
      IF NOT FOUND THEN
        RAISE SQLSTATE 'PT409' USING MESSAGE = 'order_version_conflict';
      END IF;
    END IF;
    INSERT INTO api.team_order_receipts(tenant_id, subject, request_key, payload, response)
      VALUES(team_id, actor, request_key, intended, to_jsonb(result));
  END IF;
  PERFORM set_config('gregale.team_order_write', COALESCE(previous_scope, ''), true);
  RETURN NEXT result;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.team_order_write', COALESCE(previous_scope, ''), true);
  RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.save_team_order_once(uuid,text,uuid,integer,integer,api.order_status) FROM PUBLIC;
COMMENT ON FUNCTION api.save_team_order_once(uuid,text,uuid,integer,integer,api.order_status) IS '@gregale:rpc';
