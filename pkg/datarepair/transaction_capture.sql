-- Optional SDK transaction contract. Retain this metadata with operation receipts.
CREATE TABLE IF NOT EXISTS gregale_repair.capture_receipts (
 operation_id uuid PRIMARY KEY REFERENCES gregale_repair.operations(id),
 protocol text NOT NULL CHECK(protocol IN ('managed','customer')),
 scope jsonb NOT NULL CHECK(jsonb_typeof(scope)='object'),
 release text NOT NULL CHECK(octet_length(release) BETWEEN 1 AND __CAPTURE_CONTEXT_BYTES__),
 qualified_table text NOT NULL CHECK(octet_length(qualified_table) BETWEEN 1 AND __SOURCE_MAX_FILTER_BYTES__),
 enrollment_id uuid NOT NULL,
 schema_fingerprint text NOT NULL,
 transaction_id text NOT NULL,
 request_digest bytea NOT NULL CHECK(octet_length(request_digest)=32),
 response_digest bytea NOT NULL CHECK(octet_length(response_digest)=32),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE OR REPLACE FUNCTION gregale_repair.protect_capture_receipt() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE o gregale_repair.operations; qualified text; receipt_ok boolean;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'repair_capture_receipt_immutable' USING ERRCODE='GR002'; END IF;
 SELECT * INTO o FROM gregale_repair.operations WHERE id=NEW.operation_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'repair_capture_missing' USING ERRCODE='GR001'; END IF;
 IF NOT o.finalized OR o.transaction_id<>pg_current_xact_id()::text OR
  nullif(current_setting('gregale.repair_operation',true),'') IS NOT NULL THEN
  RAISE EXCEPTION 'repair_capture_incomplete' USING ERRCODE='GR003';
 END IF;
 SELECT qualified_name INTO qualified FROM gregale_repair.enrollments WHERE enrollment_id=o.enrollment_id;
 IF o.repair_of IS NOT NULL OR o.payload_purged_at IS NOT NULL OR NEW.scope IS DISTINCT FROM o.scope OR
  NEW.release IS DISTINCT FROM o.release OR NEW.qualified_table IS DISTINCT FROM qualified OR
  NEW.enrollment_id IS DISTINCT FROM o.enrollment_id OR NEW.schema_fingerprint IS DISTINCT FROM o.schema_fingerprint OR
  NEW.transaction_id IS DISTINCT FROM o.transaction_id THEN
  RAISE EXCEPTION 'repair_capture_mismatch' USING ERRCODE='GR002';
 END IF;
 -- xmin is compared only against this open transaction, never retained or used
 -- for historical replay. This proves the receipt was inserted with the capture.
 IF NEW.protocol='managed' THEN
  SELECT r.account_id::text=NEW.scope->>'account_id' AND r.app_id::text=NEW.scope->>'app_id' AND
   coalesce(r.platform_tenant_id::text,'')=coalesce(NEW.scope->>'platform_tenant_id','') AND
   r.request_digest=NEW.request_digest AND sha256(convert_to(r.response_body,'UTF8'))=NEW.response_digest AND
   r.xmin=pg_current_xact_id()::xid INTO receipt_ok FROM public.gregale_operation_inbox r WHERE r.operation_id=NEW.operation_id;
 ELSIF NEW.protocol='customer' THEN
  SELECT r.account_id::text=NEW.scope->>'account_id' AND r.app_id::text=NEW.scope->>'app_id' AND
   r.platform_tenant_id::text=NEW.scope->>'platform_tenant_id' AND r.request_digest=NEW.request_digest AND
   sha256(convert_to(r.response_body,'UTF8'))=NEW.response_digest AND r.xmin=pg_current_xact_id()::xid
   INTO receipt_ok FROM public.gregale_customer_operation_inbox r WHERE r.operation_id=NEW.operation_id;
 END IF;
 IF receipt_ok IS DISTINCT FROM true THEN RAISE EXCEPTION 'repair_capture_mismatch' USING ERRCODE='GR002'; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS gregale_repair_protect_capture_receipt ON gregale_repair.capture_receipts;
CREATE TRIGGER gregale_repair_protect_capture_receipt BEFORE INSERT OR UPDATE OR DELETE ON gregale_repair.capture_receipts
FOR EACH ROW EXECUTE FUNCTION gregale_repair.protect_capture_receipt();

CREATE OR REPLACE FUNCTION gregale_repair.capture_transaction(operation uuid, expected_scope jsonb, release_version text,
 qualified_table_name text, receipt_protocol text, expected_request_digest bytea, expected_response_digest bytea, phase text) RETURNS void
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE saved gregale_repair.capture_receipts; target regclass; enrollment uuid;
BEGIN
 IF operation IS NULL OR expected_scope IS NULL OR release_version IS NULL OR qualified_table_name IS NULL OR
  receipt_protocol IS NULL OR receipt_protocol NOT IN ('managed','customer') OR phase IS NULL OR phase NOT IN ('begin','seal','replay') OR
  octet_length(release_version) NOT BETWEEN 1 AND __CAPTURE_CONTEXT_BYTES__ OR
  octet_length(qualified_table_name) NOT BETWEEN 1 AND __SOURCE_MAX_FILTER_BYTES__ OR
  expected_request_digest IS NULL OR octet_length(expected_request_digest)<>32 OR
  (phase<>'begin' AND (expected_response_digest IS NULL OR octet_length(expected_response_digest)<>32)) THEN
  RAISE EXCEPTION 'repair_capture_mismatch' USING ERRCODE='GR002';
 END IF;
 IF phase='replay' THEN
  SELECT * INTO saved FROM gregale_repair.capture_receipts WHERE operation_id=operation;
  IF NOT FOUND THEN RAISE EXCEPTION 'repair_capture_missing' USING ERRCODE='GR001'; END IF;
  IF saved.protocol IS DISTINCT FROM receipt_protocol OR saved.scope IS DISTINCT FROM expected_scope OR
   saved.release IS DISTINCT FROM release_version OR saved.qualified_table IS DISTINCT FROM qualified_table_name OR
   saved.request_digest IS DISTINCT FROM expected_request_digest OR saved.response_digest IS DISTINCT FROM expected_response_digest THEN
   RAISE EXCEPTION 'repair_capture_mismatch' USING ERRCODE='GR002';
  END IF;
  RETURN;
 END IF;
 -- Fence changes to receipt protection through the complete owned transaction.
 LOCK TABLE gregale_repair.capture_receipts IN ROW EXCLUSIVE MODE;
 IF current_setting('session_replication_role')<>'origin' OR NOT EXISTS(
  SELECT 1 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid
  WHERE c.oid='gregale_repair.capture_receipts'::regclass AND c.relkind='r' AND NOT c.relispartition AND
   NOT c.relrowsecurity AND NOT c.relforcerowsecurity AND t.tgname='gregale_repair_protect_capture_receipt' AND
   t.tgfoid='gregale_repair.protect_capture_receipt()'::regprocedure AND t.tgtype=31 AND t.tgenabled IN ('O','A') AND
   t.tgnargs=0 AND t.tgattr=''::int2vector AND t.tgqual IS NULL AND t.tgconstraint=0 AND NOT t.tgisinternal
 ) THEN RAISE EXCEPTION 'repair_capture_unavailable' USING ERRCODE='GR004'; END IF;
 IF phase='begin' THEN
  -- Validate both privileges before the business callback, using no payload read.
  PERFORM operation_id FROM gregale_repair.capture_receipts LIMIT 0;
  IF NOT has_table_privilege('gregale_repair.capture_receipts','INSERT') THEN
   RAISE EXCEPTION 'repair_capture_unavailable' USING ERRCODE='GR004';
  END IF;
  SELECT relation INTO target FROM gregale_repair.enrollments WHERE qualified_name=qualified_table_name;
  IF target IS NULL THEN RAISE EXCEPTION 'repair_capture_mismatch' USING ERRCODE='GR002'; END IF;
  PERFORM gregale_repair.begin_operation(operation,expected_scope,release_version,target);
 ELSE
  SELECT o.enrollment_id INTO enrollment FROM gregale_repair.operations o WHERE o.id=operation;
  IF NOT FOUND THEN RAISE EXCEPTION 'repair_capture_missing' USING ERRCODE='GR001'; END IF;
  PERFORM gregale_repair.check_enrollment(enrollment);
  INSERT INTO gregale_repair.capture_receipts(operation_id,protocol,scope,release,qualified_table,enrollment_id,schema_fingerprint,transaction_id,request_digest,response_digest)
  SELECT o.id,receipt_protocol,expected_scope,release_version,qualified_table_name,o.enrollment_id,o.schema_fingerprint,o.transaction_id,
   expected_request_digest,expected_response_digest FROM gregale_repair.operations o WHERE o.id=operation;
  IF NOT FOUND THEN RAISE EXCEPTION 'repair_capture_missing' USING ERRCODE='GR001'; END IF;
 END IF;
END $$;
