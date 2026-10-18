-- Review this business-operation example before adding it as a numbered migration.
CREATE SCHEMA IF NOT EXISTS api;
CREATE TABLE api.__OP___v2_inventory (
 tenant_id text NOT NULL, sku text NOT NULL CHECK (sku ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$'),
 stock integer NOT NULL CHECK (stock BETWEEN 0 AND 1000000),
 unit_price_cents integer NOT NULL CHECK (unit_price_cents BETWEEN 0 AND 1000000000),
 PRIMARY KEY (tenant_id,sku)
);
CREATE TABLE api.__OP___v2_orders (
 tenant_id text NOT NULL, id uuid NOT NULL DEFAULT gen_random_uuid(), created_by text NOT NULL,
 request_key uuid NOT NULL,
 total_cents bigint NOT NULL DEFAULT 0 CHECK (total_cents BETWEEN 0 AND 100000000000000),
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY (tenant_id,id),
 UNIQUE (tenant_id,created_by,request_key)
);
CREATE TABLE api.__OP___v2_items (
 tenant_id text NOT NULL, order_id uuid NOT NULL, sku text NOT NULL,
 quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 1000),
 unit_price_cents integer NOT NULL CHECK (unit_price_cents BETWEEN 0 AND 1000000000),
 PRIMARY KEY (tenant_id,order_id,sku),
 FOREIGN KEY (tenant_id,order_id) REFERENCES api.__OP___v2_orders(tenant_id,id),
 FOREIGN KEY (tenant_id,sku) REFERENCES api.__OP___v2_inventory(tenant_id,sku)
);
ALTER TABLE api.__OP___v2_inventory ENABLE ROW LEVEL SECURITY;
ALTER TABLE api.__OP___v2_inventory FORCE ROW LEVEL SECURITY;
ALTER TABLE api.__OP___v2_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE api.__OP___v2_orders FORCE ROW LEVEL SECURITY;
ALTER TABLE api.__OP___v2_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE api.__OP___v2_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_reads ON api.__OP___v2_inventory FOR SELECT USING (tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id');
CREATE POLICY operation_writes ON api.__OP___v2_inventory FOR ALL USING (
 tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id' AND tenant_id=current_setting('gregale.transaction___OP___v2',true)
) WITH CHECK (tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id' AND tenant_id=current_setting('gregale.transaction___OP___v2',true));
CREATE POLICY tenant_reads ON api.__OP___v2_orders FOR SELECT USING (tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id');
CREATE POLICY operation_writes ON api.__OP___v2_orders FOR ALL USING (
 tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id' AND tenant_id=current_setting('gregale.transaction___OP___v2',true) AND created_by=current_setting('request.jwt.claims',true)::jsonb->>'sub'
) WITH CHECK (tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id' AND tenant_id=current_setting('gregale.transaction___OP___v2',true) AND created_by=current_setting('request.jwt.claims',true)::jsonb->>'sub');
CREATE POLICY tenant_reads ON api.__OP___v2_items FOR SELECT USING (tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id');
CREATE POLICY operation_writes ON api.__OP___v2_items FOR ALL USING (
 tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id' AND tenant_id=current_setting('gregale.transaction___OP___v2',true)
) WITH CHECK (tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id' AND tenant_id=current_setting('gregale.transaction___OP___v2',true));

-- Success receipts have no business-row foreign key: later changes cannot
-- erase the original outcome. Fixed window mirrors pkg/api/limits.go (ADR-1673).
CREATE TABLE api.__OP___v2_receipts (
 tenant_id text NOT NULL, created_by text NOT NULL,
 request_key uuid NOT NULL CHECK (substr(request_key::text,15,1)='7' AND substr(request_key::text,20,1) IN ('8','9','a','b')),
 request jsonb NOT NULL CHECK (jsonb_typeof(request)='array'),
 response jsonb NOT NULL CHECK (jsonb_typeof(response)='object'),
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz GENERATED ALWAYS AS (
  to_timestamp(((('x'||substr(replace(request_key::text,'-',''),1,12))::bit(48)::bigint
   + 86400000)::double precision)/1000)) STORED,
 PRIMARY KEY (tenant_id,created_by,request_key)
);
CREATE INDEX __OP___v2_receipts_expiry ON api.__OP___v2_receipts(expires_at,tenant_id,created_by,request_key);
ALTER TABLE api.__OP___v2_receipts ENABLE ROW LEVEL SECURITY;
CREATE POLICY receipt_reads ON api.__OP___v2_receipts FOR SELECT USING (
 tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id'
 AND created_by=current_setting('request.jwt.claims',true)::jsonb->>'sub'
 AND current_setting('gregale.transaction___OP___v2_receipt',true) IN ('write','read'));
CREATE POLICY receipt_inserts ON api.__OP___v2_receipts FOR INSERT WITH CHECK (
 tenant_id=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id'
 AND created_by=current_setting('request.jwt.claims',true)::jsonb->>'sub'
 AND current_setting('gregale.transaction___OP___v2_receipt',true)='write');
CREATE FUNCTION api.__OP___guard_receipt_v2() RETURNS trigger
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
BEGIN
 IF TG_OP='UPDATE' THEN RAISE SQLSTATE 'PT409' USING MESSAGE='idempotency_receipt_immutable'; END IF;
 IF OLD.expires_at>clock_timestamp() THEN RAISE SQLSTATE 'PT409' USING MESSAGE='idempotency_replay_window_open'; END IF;
 RETURN OLD;
END;
$$;
REVOKE ALL ON FUNCTION api.__OP___guard_receipt_v2() FROM PUBLIC;
CREATE TRIGGER __OP___guard_receipt_v2 BEFORE UPDATE OR DELETE ON api.__OP___v2_receipts
 FOR EACH ROW EXECUTE FUNCTION api.__OP___guard_receipt_v2();
CREATE TYPE api.__OP___v2_result AS (
 request_key uuid, tenant_id text, created_by text, order_id uuid,
 total_cents bigint, created_at timestamptz, lines jsonb
);

CREATE FUNCTION api."__OP___v2"(request_key uuid,skus text[],quantities integer[])
RETURNS SETOF api.__OP___v2_result LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog
SET default_transaction_isolation='read committed' AS $$
DECLARE
 tenant text:=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id';
 actor text:=current_setting('request.jwt.claims',true)::jsonb->>'sub';
 prior_scope text:=current_setting('gregale.transaction___OP___v2',true);
 prior_receipt_scope text:=current_setting('gregale.transaction___OP___v2_receipt',true);
 payload jsonb; saved api.__OP___v2_receipts; order_row api.__OP___v2_orders;
 line record; inventory api.__OP___v2_inventory; locked_count integer; total bigint:=0;
 issued_at timestamptz; expires_at timestamptz; snapshot jsonb; lines jsonb:='[]'::jsonb;
BEGIN
 IF tenant IS NULL OR tenant='' OR length(tenant)>128 OR actor IS NULL OR actor='' OR length(actor)>256 THEN
  RAISE SQLSTATE 'PT403' USING MESSAGE='transaction_identity_required';
 END IF;
 IF request_key IS NULL OR substr(request_key::text,15,1)<>'7' OR substr(request_key::text,20,1) NOT IN ('8','9','a','b') THEN
  RAISE SQLSTATE 'PT400' USING MESSAGE='idempotency_key_invalid';
 END IF;
 IF array_ndims(skus) IS DISTINCT FROM 1 OR array_ndims(quantities) IS DISTINCT FROM 1
 OR cardinality(skus) NOT BETWEEN 1 AND 100 OR cardinality(skus) IS DISTINCT FROM cardinality(quantities)
 OR EXISTS(SELECT 1 FROM unnest(skus,quantities) x(sku,quantity) WHERE sku IS NULL OR sku !~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$' OR quantity IS NULL OR quantity NOT BETWEEN 1 AND 1000)
 OR (SELECT count(DISTINCT sku) FROM unnest(skus) x(sku))<>cardinality(skus) THEN
  RAISE SQLSTATE 'PT400' USING MESSAGE='transaction_arguments_invalid';
 END IF;
 SELECT jsonb_agg(jsonb_build_object('sku',sku,'quantity',quantity) ORDER BY sku COLLATE "C") INTO payload FROM unnest(skus,quantities) x(sku,quantity);
 issued_at:=to_timestamp((('x'||substr(replace(request_key::text,'-',''),1,12))::bit(48)::bigint)::double precision/1000);
 IF issued_at>clock_timestamp()+interval '300000 milliseconds' THEN
  RAISE SQLSTATE 'PT400' USING MESSAGE='idempotency_key_in_future';
 END IF;
 expires_at:=issued_at+interval '86400000 milliseconds';
 IF clock_timestamp()>=expires_at THEN
  RAISE SQLSTATE 'PT410' USING MESSAGE='idempotency_key_expired';
 END IF;
 PERFORM set_config('gregale.transaction___OP___v2_receipt','write',true);
 PERFORM pg_advisory_xact_lock(hashtextextended(jsonb_build_array('gregale.transaction.__OP__.v2',tenant,actor,request_key)::text,0));
 IF clock_timestamp()>=expires_at THEN
  RAISE SQLSTATE 'PT410' USING MESSAGE='idempotency_key_expired';
 END IF;
 SELECT r.* INTO saved FROM api.__OP___v2_receipts r
  WHERE r.tenant_id=tenant AND r.created_by=actor AND r.request_key="__OP___v2".request_key;
 IF FOUND THEN
  IF saved.request IS DISTINCT FROM payload THEN RAISE SQLSTATE 'PT409' USING MESSAGE='idempotency_payload_conflict'; END IF;
  snapshot:=saved.response;
 ELSE
  PERFORM set_config('gregale.transaction___OP___v2',tenant,true);
  PERFORM 1 FROM api.__OP___v2_inventory i WHERE i.tenant_id=tenant AND i.sku=ANY(skus) ORDER BY i.sku COLLATE "C" FOR UPDATE;
  GET DIAGNOSTICS locked_count=ROW_COUNT;
 IF clock_timestamp()>=expires_at THEN
  RAISE SQLSTATE 'PT410' USING MESSAGE='idempotency_key_expired';
 END IF;
  IF locked_count<>cardinality(skus) THEN RAISE SQLSTATE 'PT409' USING MESSAGE='transaction_inventory_unavailable'; END IF;
  INSERT INTO api.__OP___v2_orders(tenant_id,created_by,request_key) VALUES(tenant,actor,request_key) RETURNING * INTO order_row;
  FOR line IN SELECT x.sku,x.quantity FROM unnest(skus,quantities) x(sku,quantity) ORDER BY x.sku COLLATE "C" LOOP
   SELECT i.* INTO inventory FROM api.__OP___v2_inventory i WHERE i.tenant_id=tenant AND i.sku=line.sku;
   IF inventory.stock<line.quantity THEN RAISE SQLSTATE 'PT409' USING MESSAGE='transaction_inventory_unavailable'; END IF;
   UPDATE api.__OP___v2_inventory i SET stock=i.stock-line.quantity WHERE i.tenant_id=tenant AND i.sku=line.sku;
   INSERT INTO api.__OP___v2_items(tenant_id,order_id,sku,quantity,unit_price_cents) VALUES(tenant,order_row.id,line.sku,line.quantity,inventory.unit_price_cents);
   lines:=lines||jsonb_build_array(jsonb_build_object('sku',line.sku,'quantity',line.quantity,'unit_price_cents',inventory.unit_price_cents));
   total:=total+line.quantity::bigint*inventory.unit_price_cents;
 IF clock_timestamp()>=expires_at THEN
  RAISE SQLSTATE 'PT410' USING MESSAGE='idempotency_key_expired';
 END IF;
  END LOOP;
  UPDATE api.__OP___v2_orders o SET total_cents=total WHERE o.tenant_id=tenant AND o.id=order_row.id RETURNING * INTO order_row;
  snapshot:=to_jsonb(ROW(request_key,tenant,actor,order_row.id,total,order_row.created_at,lines)::api.__OP___v2_result);
  INSERT INTO api.__OP___v2_receipts(tenant_id,created_by,request_key,request,response) VALUES(tenant,actor,request_key,payload,snapshot);
 END IF;
 IF clock_timestamp()>=expires_at THEN
  RAISE SQLSTATE 'PT410' USING MESSAGE='idempotency_key_expired';
 END IF;
 PERFORM set_config('gregale.transaction___OP___v2',coalesce(prior_scope,''),true);
 PERFORM set_config('gregale.transaction___OP___v2_receipt',coalesce(prior_receipt_scope,''),true);
 RETURN NEXT jsonb_populate_record(NULL::api.__OP___v2_result,snapshot);
 RETURN;
EXCEPTION WHEN OTHERS THEN
 PERFORM set_config('gregale.transaction___OP___v2',coalesce(prior_scope,''),true);
 PERFORM set_config('gregale.transaction___OP___v2_receipt',coalesce(prior_receipt_scope,''),true);
 RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api."__OP___v2"(uuid,text[],integer[]) FROM PUBLIC;
COMMENT ON FUNCTION api."__OP___v2"(uuid,text[],integer[]) IS '@gregale:rpc';

CREATE FUNCTION api.read___OP___receipt_v2(request_key uuid,skus text[],quantities integer[])
RETURNS SETOF api.__OP___v2_result LANGUAGE plpgsql STABLE SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE
 tenant text:=current_setting('request.jwt.claims',true)::jsonb->>'tenant_id';
 actor text:=current_setting('request.jwt.claims',true)::jsonb->>'sub';
 prior_receipt_scope text:=current_setting('gregale.transaction___OP___v2_receipt',true);
 payload jsonb; saved api.__OP___v2_receipts; issued_at timestamptz;
BEGIN
 IF tenant IS NULL OR tenant='' OR length(tenant)>128 OR actor IS NULL OR actor='' OR length(actor)>256 THEN
  RAISE SQLSTATE 'PT403' USING MESSAGE='transaction_identity_required';
 END IF;
 IF request_key IS NULL OR substr(request_key::text,15,1)<>'7' OR substr(request_key::text,20,1) NOT IN ('8','9','a','b') THEN
  RAISE SQLSTATE 'PT400' USING MESSAGE='idempotency_key_invalid';
 END IF;
 IF array_ndims(skus) IS DISTINCT FROM 1 OR array_ndims(quantities) IS DISTINCT FROM 1
 OR cardinality(skus) NOT BETWEEN 1 AND 100 OR cardinality(skus) IS DISTINCT FROM cardinality(quantities)
 OR EXISTS(SELECT 1 FROM unnest(skus,quantities) x(sku,quantity) WHERE sku IS NULL OR sku !~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$' OR quantity IS NULL OR quantity NOT BETWEEN 1 AND 1000)
 OR (SELECT count(DISTINCT sku) FROM unnest(skus) x(sku))<>cardinality(skus) THEN
  RAISE SQLSTATE 'PT400' USING MESSAGE='transaction_arguments_invalid';
 END IF;
 SELECT jsonb_agg(jsonb_build_object('sku',sku,'quantity',quantity) ORDER BY sku COLLATE "C") INTO payload FROM unnest(skus,quantities) x(sku,quantity);
 issued_at:=to_timestamp((('x'||substr(replace(request_key::text,'-',''),1,12))::bit(48)::bigint)::double precision/1000);
 IF issued_at>clock_timestamp()+interval '300000 milliseconds' THEN
  RAISE SQLSTATE 'PT400' USING MESSAGE='idempotency_key_in_future';
 END IF;
 PERFORM set_config('gregale.transaction___OP___v2_receipt','read',true);
 SELECT r.* INTO saved FROM api.__OP___v2_receipts r
  WHERE r.tenant_id=tenant AND r.created_by=actor AND r.request_key=read___OP___receipt_v2.request_key;
 IF FOUND THEN
  IF saved.request IS DISTINCT FROM payload THEN RAISE SQLSTATE 'PT409' USING MESSAGE='idempotency_payload_conflict'; END IF;
  RETURN NEXT jsonb_populate_record(NULL::api.__OP___v2_result,saved.response);
 ELSIF clock_timestamp()>=issued_at+interval '86400000 milliseconds' THEN
  RAISE SQLSTATE 'PT410' USING MESSAGE='idempotency_key_expired';
 END IF;
 PERFORM set_config('gregale.transaction___OP___v2_receipt',coalesce(prior_receipt_scope,''),true);
 RETURN;
EXCEPTION WHEN OTHERS THEN
 PERFORM set_config('gregale.transaction___OP___v2_receipt',coalesce(prior_receipt_scope,''),true);
 RAISE;
END;
$$;
REVOKE ALL ON FUNCTION api.read___OP___receipt_v2(uuid,text[],integer[]) FROM PUBLIC;
COMMENT ON FUNCTION api.read___OP___receipt_v2(uuid,text[],integer[]) IS '@gregale:rpc';
