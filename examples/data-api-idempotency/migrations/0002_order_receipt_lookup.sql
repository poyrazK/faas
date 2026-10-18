-- Read the immutable original response without creating or replaying an order.
CREATE FUNCTION api.read_order_receipt(request_key uuid, order_total integer)
RETURNS SETOF api.orders
LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path = pg_catalog
AS $$
DECLARE
  claims jsonb := current_setting('request.jwt.claims', true)::jsonb;
  subject text := NULLIF(claims ->> 'sub', '');
  team text := NULLIF(claims ->> 'tenant', '');
  receipt api.orders;
BEGIN
  IF subject IS NULL OR team IS NULL OR request_key IS NULL OR order_total IS NULL OR order_total < 0 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE = 'invalid_order_receipt_lookup';
  END IF;
  SELECT o.* INTO receipt FROM api.orders o
    WHERE o.owner=subject AND o.tenant=team AND o.request_key=read_order_receipt.request_key;
  IF FOUND THEN
    IF receipt.total IS DISTINCT FROM order_total THEN
      RAISE SQLSTATE 'PT409' USING MESSAGE = 'idempotency_payload_conflict';
    END IF;
    RETURN NEXT receipt;
  END IF;
  RETURN;
END;
$$;
REVOKE ALL ON FUNCTION api.read_order_receipt(uuid, integer) FROM PUBLIC;
COMMENT ON FUNCTION api.read_order_receipt(uuid, integer) IS '@gregale:rpc';
