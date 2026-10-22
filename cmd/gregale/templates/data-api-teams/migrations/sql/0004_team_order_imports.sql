-- Application import bounds: 1 MiB manifest, 1,000 rows, 20 rows per transaction.
CREATE TABLE api.team_order_imports (
  import_id uuid NOT NULL,
  tenant_id text NOT NULL,
  actor text NOT NULL,
  source_name text NOT NULL CHECK(length(source_name) BETWEEN 1 AND 128),
  manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='array'),
  total integer NOT NULL CHECK(total BETWEEN 1 AND 1000),
  processed integer NOT NULL DEFAULT 0 CHECK(processed>=0),
  inserted integer NOT NULL DEFAULT 0 CHECK(inserted>=0),
  skipped integer NOT NULL DEFAULT 0 CHECK(skipped>=0),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  completed_at timestamptz,
  PRIMARY KEY(tenant_id,actor,import_id),
  CHECK(processed=inserted+skipped AND processed<=total),
  CHECK((completed_at IS NOT NULL)=(processed=total))
);
ALTER TABLE api.team_order_imports ENABLE ROW LEVEL SECURITY;
CREATE POLICY import_reads ON api.team_order_imports FOR SELECT USING (
  EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_order_imports.tenant_id
    AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'
    AND (m.role='owner' OR (m.role='admin' AND actor=m.subject)))
);
CREATE POLICY import_creates ON api.team_order_imports FOR INSERT WITH CHECK (
  tenant_id=current_setting('gregale.order_import_write',true)
  AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub'
);
CREATE POLICY import_updates ON api.team_order_imports FOR UPDATE USING (
  tenant_id=current_setting('gregale.order_import_write',true)
  AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub'
) WITH CHECK(tenant_id=current_setting('gregale.order_import_write',true) AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub');

CREATE FUNCTION api.start_team_order_import_once(operation_id uuid,team_id text,source_label text,items jsonb) RETURNS SETOF api.team_order_imports
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text := NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
  old_import_scope text := current_setting('gregale.order_import_write',true);
  old_order_scope text := current_setting('gregale.team_order_write',true);
  result api.team_order_imports;
  item jsonb;
  created_id uuid;
  row_inserted integer := 0;
  row_skipped integer := 0;
BEGIN
  IF caller_subject IS NULL OR team_id IS NULL OR team_id='' OR operation_id IS NULL THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_import';
  END IF;
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='import_read_committed_required'; END IF;
  -- Same lock as membership administration: authorization is checked after waiting.
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject AND m.role IN ('owner','admin')) THEN
    RAISE SQLSTATE '42501' USING MESSAGE='team_write_denied';
  END IF;
  PERFORM set_config('gregale.order_import_write',team_id,true);
  IF source_label IS NULL OR length(source_label) NOT BETWEEN 1 AND 128 OR items IS NULL OR jsonb_typeof(items)<>'array' OR octet_length(items::text)>1048576 THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_import';
  END IF;
  IF jsonb_array_length(items) NOT BETWEEN 1 AND 1000 THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_import'; END IF;
  -- Validate every row before registering a resumable plan. No order writes here.
  FOR item IN SELECT value FROM jsonb_array_elements(items) LOOP
    IF jsonb_typeof(item)<>'object' OR (SELECT count(*) FROM jsonb_object_keys(item))<>3
      OR jsonb_typeof(item->'id') IS DISTINCT FROM 'string' OR (item->>'id') !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
      OR jsonb_typeof(item->'total_cents') IS DISTINCT FROM 'number' OR (item->>'total_cents') !~ '^[0-9]+$'
      OR (item->>'total_cents')::numeric>2147483647 OR (item->>'status') NOT IN ('draft','submitted','fulfilled') OR jsonb_typeof(item->'status') IS DISTINCT FROM 'string' THEN
      RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_import_row';
    END IF;
  END LOOP;
  IF (SELECT count(*) FROM jsonb_array_elements(items))<>(SELECT count(DISTINCT value->>'id') FROM jsonb_array_elements(items)) THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='duplicate_import_id';
  END IF;
  SELECT i.* INTO result FROM api.team_order_imports i WHERE i.tenant_id=team_id AND i.actor=caller_subject AND i.import_id=operation_id;
  IF FOUND THEN
    IF result.source_name<>source_label OR result.manifest<>items THEN RAISE SQLSTATE 'PT409' USING MESSAGE='import_payload_conflict'; END IF;
  ELSE
    INSERT INTO api.team_order_imports(import_id,tenant_id,actor,source_name,manifest,total)
      VALUES(operation_id,team_id,caller_subject,source_label,items,jsonb_array_length(items)) RETURNING * INTO result;
  END IF;
  PERFORM set_config('gregale.order_import_write',COALESCE(old_import_scope,''),true);
  PERFORM set_config('gregale.team_order_write',COALESCE(old_order_scope,''),true);
  RETURN NEXT result;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.order_import_write',COALESCE(old_import_scope,''),true);
  PERFORM set_config('gregale.team_order_write',COALESCE(old_order_scope,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.start_team_order_import_once(uuid,text,text,jsonb) FROM PUBLIC;
COMMENT ON FUNCTION api.start_team_order_import_once(uuid,text,text,jsonb) IS '@gregale:rpc';

CREATE FUNCTION api.advance_team_order_import_once(operation_id uuid,team_id text,expected_processed integer) RETURNS SETOF api.team_order_imports
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog SET default_transaction_isolation='read committed' AS $$
DECLARE
  caller_subject text := NULLIF(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
  old_import_scope text := current_setting('gregale.order_import_write',true);
  old_order_scope text := current_setting('gregale.team_order_write',true);
  result api.team_order_imports;
  item jsonb;
  created_id uuid;
  row_inserted integer := 0;
  row_skipped integer := 0;
BEGIN
  IF caller_subject IS NULL OR team_id IS NULL OR team_id='' OR operation_id IS NULL THEN
    RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_order_import';
  END IF;
  IF current_setting('transaction_isolation')<>'read committed' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='import_read_committed_required'; END IF;
  -- Same lock as membership administration: authorization is checked after waiting.
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:' || team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_id AND m.subject=caller_subject AND m.role IN ('owner','admin')) THEN
    RAISE SQLSTATE '42501' USING MESSAGE='team_write_denied';
  END IF;
  PERFORM set_config('gregale.order_import_write',team_id,true);
  SELECT i.* INTO result FROM api.team_order_imports i WHERE i.tenant_id=team_id AND i.actor=caller_subject AND i.import_id=operation_id;
  IF NOT FOUND THEN RAISE SQLSTATE 'PT409' USING MESSAGE='import_unavailable'; END IF;
  IF expected_processed IS NULL OR expected_processed<0 OR expected_processed>result.processed THEN RAISE SQLSTATE 'PT409' USING MESSAGE='import_progress_conflict'; END IF;
  IF expected_processed=result.processed AND result.processed<result.total THEN
    PERFORM set_config('gregale.team_order_write','on',true);
    FOR item IN SELECT value FROM jsonb_array_elements(result.manifest) WITH ORDINALITY e(value,position)
      WHERE position>result.processed AND position<=result.processed+20 ORDER BY position LOOP
      created_id := NULL;
      -- Preserve existing orders. Duplicate IDs are counted, never overwritten.
      INSERT INTO api.team_orders(id,tenant_id,created_by,total_cents,status)
        VALUES((item->>'id')::uuid,team_id,caller_subject,(item->>'total_cents')::integer,(item->>'status')::api.order_status)
        ON CONFLICT(tenant_id,id) DO NOTHING RETURNING id INTO created_id;
      IF created_id IS NULL THEN row_skipped:=row_skipped+1; ELSE row_inserted:=row_inserted+1; END IF;
    END LOOP;
    UPDATE api.team_order_imports i SET processed=i.processed+row_inserted+row_skipped,inserted=i.inserted+row_inserted,skipped=i.skipped+row_skipped,
      completed_at=CASE WHEN i.processed+row_inserted+row_skipped=i.total THEN clock_timestamp() ELSE NULL END
      WHERE i.tenant_id=team_id AND i.actor=caller_subject AND i.import_id=operation_id RETURNING i.* INTO result;
  END IF;
  -- An older cursor is a receipt lookup, not permission to process another batch.

  PERFORM set_config('gregale.order_import_write',COALESCE(old_import_scope,''),true);
  PERFORM set_config('gregale.team_order_write',COALESCE(old_order_scope,''),true);
  RETURN NEXT result;
EXCEPTION WHEN OTHERS THEN
  PERFORM set_config('gregale.order_import_write',COALESCE(old_import_scope,''),true);
  PERFORM set_config('gregale.team_order_write',COALESCE(old_order_scope,''),true); RAISE;
END;
$$;
REVOKE EXECUTE ON FUNCTION api.advance_team_order_import_once(uuid,text,integer) FROM PUBLIC;
COMMENT ON FUNCTION api.advance_team_order_import_once(uuid,text,integer) IS '@gregale:rpc';
