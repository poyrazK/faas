-- Review this business-operation example before adding it as a numbered migration.
CREATE SCHEMA IF NOT EXISTS api;
CREATE TABLE api.__OP___inventory (
 tenant_id text NOT NULL, sku text NOT NULL CHECK (sku ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$'),
 stock integer NOT NULL CHECK (stock BETWEEN 0 AND 1000000),
 unit_price_cents integer NOT NULL CHECK (unit_price_cents BETWEEN 0 AND 1000000000),
 PRIMARY KEY (tenant_id,sku)
);
CREATE TABLE api.__OP___orders (
 tenant_id text NOT NULL, id uuid NOT NULL DEFAULT gen_random_uuid(), created_by text NOT NULL,
 request_key uuid NOT NULL, request_payload jsonb NOT NULL,
 total_cents bigint NOT NULL DEFAULT 0 CHECK (total_cents BETWEEN 0 AND 100000000000000),
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (tenant_id,id),
 UNIQUE (tenant_id,created_by,request_key)
);
CREATE TABLE api.__OP___items (
 tenant_id text NOT NULL, order_id uuid NOT NULL, sku text NOT NULL,
 quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 1000),
 unit_price_cents integer NOT NULL CHECK (unit_price_cents BETWEEN 0 AND 1000000000),
 PRIMARY KEY (tenant_id,order_id,sku),
 FOREIGN KEY (tenant_id,order_id) REFERENCES api.__OP___orders(tenant_id,id),
 FOREIGN KEY (tenant_id,sku) REFERENCES api.__OP___inventory(tenant_id,sku)
);
ALTER TABLE api.__OP___inventory ENABLE ROW LEVEL SECURITY;
ALTER TABLE api.__OP___inventory FORCE ROW LEVEL SECURITY;
ALTER TABLE api.__OP___orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE api.__OP___orders FORCE ROW LEVEL SECURITY;
ALTER TABLE api.__OP___items ENABLE ROW LEVEL SECURITY;
ALTER TABLE api.__OP___items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_reads ON api.__OP___inventory FOR SELECT USING (tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id');
CREATE POLICY operation_writes ON api.__OP___inventory FOR ALL USING (
 tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id' AND tenant_id=current_setting('gregale.transaction___OP__',true)
) WITH CHECK (tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id' AND tenant_id=current_setting('gregale.transaction___OP__',true));
CREATE POLICY tenant_reads ON api.__OP___orders FOR SELECT USING (tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id');
CREATE POLICY operation_writes ON api.__OP___orders FOR ALL USING (
 tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id' AND tenant_id=current_setting('gregale.transaction___OP__',true) AND created_by=current_setting('request.jwt.claims',true)::jsonb->>'sub'
) WITH CHECK (tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id' AND tenant_id=current_setting('gregale.transaction___OP__',true) AND created_by=current_setting('request.jwt.claims',true)::jsonb->>'sub');
CREATE POLICY tenant_reads ON api.__OP___items FOR SELECT USING (tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id');
CREATE POLICY operation_writes ON api.__OP___items FOR ALL USING (
 tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id' AND tenant_id=current_setting('gregale.transaction___OP__',true)
) WITH CHECK (tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id' AND tenant_id=current_setting('gregale.transaction___OP__',true));

CREATE FUNCTION api."__OP__"(request_key uuid,skus text[],quantities integer[])
RETURNS SETOF api.__OP___orders LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE
 tenant text:=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id';
 actor text:=current_setting('request.jwt.claims',true)::jsonb->>'sub';
 prior_scope text:=current_setting('gregale.transaction___OP__',true);
 payload jsonb; receipt api.__OP___orders; line record; inventory api.__OP___inventory;
 locked_count integer; total bigint:=0;
BEGIN
 IF tenant IS NULL OR tenant='' OR length(tenant)>128 OR actor IS NULL OR actor='' OR length(actor)>256 THEN
  RAISE SQLSTATE 'PT403' USING MESSAGE='transaction_identity_required';
 END IF;
 IF request_key IS NULL OR array_ndims(skus) IS DISTINCT FROM 1 OR array_ndims(quantities) IS DISTINCT FROM 1
 OR cardinality(skus) NOT BETWEEN 1 AND 100 OR cardinality(skus) IS DISTINCT FROM cardinality(quantities)
 OR EXISTS(SELECT 1 FROM unnest(skus,quantities) x(sku,quantity) WHERE sku IS NULL OR sku !~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$' OR quantity IS NULL OR quantity NOT BETWEEN 1 AND 1000)
 OR (SELECT count(DISTINCT sku) FROM unnest(skus) x(sku))<>cardinality(skus) THEN
  RAISE SQLSTATE 'PT400' USING MESSAGE='transaction_arguments_invalid';
 END IF;
 SELECT jsonb_agg(jsonb_build_array(sku,quantity) ORDER BY sku) INTO payload FROM unnest(skus,quantities) x(sku,quantity);
 -- Same tenant/actor/key serializes retries; stock rows serialize competing keys.
 PERFORM pg_advisory_xact_lock(hashtextextended(jsonb_build_array('__OP__',tenant,actor,request_key)::text,0));
 SELECT * INTO receipt FROM api.__OP___orders o WHERE o.tenant_id=tenant AND o.created_by=actor AND o.request_key= "__OP__".request_key;
 IF FOUND THEN
  IF receipt.request_payload IS DISTINCT FROM payload THEN RAISE SQLSTATE 'PT409' USING MESSAGE='transaction_key_reused'; END IF;
  RETURN NEXT receipt; RETURN;
 END IF;
 PERFORM set_config('gregale.transaction___OP__',tenant,true);
 INSERT INTO api.__OP___orders(tenant_id,created_by,request_key,request_payload) VALUES(tenant,actor,request_key,payload) RETURNING * INTO receipt;
 -- Canonical lock order prevents inverse item ordering from causing deadlocks.
 PERFORM 1 FROM api.__OP___inventory i WHERE i.tenant_id=tenant AND i.sku=ANY(skus) ORDER BY i.sku FOR UPDATE;
 GET DIAGNOSTICS locked_count=ROW_COUNT;
 IF locked_count<>cardinality(skus) THEN RAISE SQLSTATE 'PT409' USING MESSAGE='transaction_inventory_unavailable'; END IF;
 FOR line IN SELECT sku,quantity FROM unnest(skus,quantities) x(sku,quantity) ORDER BY sku LOOP
  SELECT * INTO inventory FROM api.__OP___inventory i WHERE i.tenant_id=tenant AND i.sku=line.sku;
  IF inventory.stock<line.quantity THEN RAISE SQLSTATE 'PT409' USING MESSAGE='transaction_inventory_unavailable'; END IF;
  UPDATE api.__OP___inventory i SET stock=i.stock-line.quantity WHERE i.tenant_id=tenant AND i.sku=line.sku;
  INSERT INTO api.__OP___items(tenant_id,order_id,sku,quantity,unit_price_cents) VALUES(tenant,receipt.id,line.sku,line.quantity,inventory.unit_price_cents);
  total:=total+line.quantity::bigint*inventory.unit_price_cents;
 END LOOP;
 UPDATE api.__OP___orders o SET total_cents=total WHERE o.tenant_id=tenant AND o.id=receipt.id RETURNING * INTO receipt;
 PERFORM set_config('gregale.transaction___OP__',COALESCE(prior_scope,''),true);
 RETURN NEXT receipt;
END;
$$;
REVOKE ALL ON FUNCTION api."__OP__"(uuid,text[],integer[]) FROM PUBLIC;
COMMENT ON FUNCTION api."__OP__"(uuid,text[],integer[]) IS '@gregale:rpc';
