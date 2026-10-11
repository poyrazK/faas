CREATE SCHEMA IF NOT EXISTS api;
CREATE TABLE api.orders (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  owner text NOT NULL,
  tenant text NOT NULL,
  request_key uuid NOT NULL,
  total integer NOT NULL CHECK (total >= 0),
  CONSTRAINT orders_request_unique UNIQUE (owner, tenant, request_key)
);
ALTER TABLE api.orders ENABLE ROW LEVEL SECURITY;
CREATE POLICY own_orders ON api.orders
  USING (owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND tenant = current_setting('request.jwt.claims', true)::jsonb ->> 'tenant')
  WITH CHECK (owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub'
    AND tenant = current_setting('request.jwt.claims', true)::jsonb ->> 'tenant');

-- The committed order is also its durable response receipt. Prevent ordinary
-- REST writes from changing/deleting it and allowing a key to be reused.
CREATE FUNCTION api.immutable_order() RETURNS trigger
  LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
  RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_receipt_immutable';
END;
$$;
CREATE TRIGGER immutable_receipt BEFORE UPDATE OR DELETE ON api.orders
  FOR EACH ROW EXECUTE FUNCTION api.immutable_order();
REVOKE EXECUTE ON FUNCTION api.immutable_order() FROM PUBLIC;

CREATE FUNCTION api.create_order_once(request_key uuid, order_total integer)
  RETURNS SETOF api.orders LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE
  claims jsonb := current_setting('request.jwt.claims', true)::jsonb;
  subject text := NULLIF(claims ->> 'sub', '');
  team text := NULLIF(claims ->> 'tenant', '');
  receipt api.orders;
BEGIN
  IF subject IS NULL OR team IS NULL OR request_key IS NULL OR order_total IS NULL OR order_total < 0 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'invalid_idempotent_order';
  END IF;
  INSERT INTO api.orders(owner,tenant,request_key,total)
    VALUES(subject,team,request_key,order_total)
    ON CONFLICT ON CONSTRAINT orders_request_unique DO NOTHING;
  -- READ COMMITTED obtains a fresh snapshot after a competing insertion commits.
  SELECT o.* INTO STRICT receipt FROM api.orders o
    WHERE o.owner=subject AND o.tenant=team AND o.request_key=create_order_once.request_key;
  IF receipt.total <> order_total THEN
    RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict';
  END IF;
  RETURN NEXT receipt;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.create_order_once(uuid,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.create_order_once(uuid,integer) IS '@gregale:rpc';
