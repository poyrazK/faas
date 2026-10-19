-- Metadata-only discovery. Installed explicitly by the owner, including indexes.
CREATE INDEX IF NOT EXISTS gregale_repair_sources_scope
 ON gregale_repair.operations(scope,created_at DESC,id DESC) WHERE finalized;
CREATE INDEX IF NOT EXISTS gregale_repair_sources_release
 ON gregale_repair.operations(scope,release,created_at DESC,id DESC) WHERE finalized;
CREATE INDEX IF NOT EXISTS gregale_repair_sources_table
 ON gregale_repair.operations(scope,enrollment_id,created_at DESC,id DESC) WHERE finalized;
CREATE INDEX IF NOT EXISTS gregale_repair_sources_receipts
 ON gregale_repair.plans(source_operation_id,scope,applied_at DESC,id DESC) WHERE applied_at IS NOT NULL;

CREATE OR REPLACE FUNCTION gregale_repair.list_sources(expected_scope jsonb, release_filter text,
 table_filter text, since_at timestamptz, before_at timestamptz,
 after_created timestamptz, after_id uuid, page_size integer) RETURNS jsonb
LANGUAGE plpgsql STABLE SET search_path = pg_catalog AS $$
DECLARE result jsonb;
BEGIN
 IF expected_scope IS NULL OR jsonb_typeof(expected_scope)<>'object' OR
  NOT(expected_scope ?& ARRAY['account_id','app_id','environment']) OR
  expected_scope-'account_id'-'app_id'-'environment'-'platform_tenant_id'<>'{}'::jsonb OR
  EXISTS(SELECT 1 FROM jsonb_each(expected_scope) kv WHERE jsonb_typeof(kv.value)<>'string' OR length(kv.value#>>'{}') NOT BETWEEN 1 AND 256) OR
  page_size IS NULL OR page_size NOT BETWEEN 1 AND __SOURCE_MAX_PAGE__ OR
  octet_length(release_filter)>__SOURCE_MAX_FILTER_BYTES__ OR octet_length(table_filter)>__SOURCE_MAX_FILTER_BYTES__ OR
  (since_at IS NOT NULL AND before_at IS NOT NULL AND since_at>=before_at) OR
  (after_created IS NULL)<>(after_id IS NULL) THEN
  RAISE EXCEPTION 'repair_invalid_source_query' USING ERRCODE='22023';
 END IF;
 -- Select bounded identities before reading counts/receipt metadata. Never
 -- inspect business rows, captured values or plan documents during discovery.
 WITH page AS MATERIALIZED (
  SELECT o.*,e.qualified_name FROM gregale_repair.operations o
  JOIN gregale_repair.enrollments e ON e.enrollment_id=o.enrollment_id
  WHERE o.scope=expected_scope AND o.finalized
   AND (release_filter IS NULL OR o.release=release_filter)
   AND (table_filter IS NULL OR e.qualified_name=table_filter)
   AND (since_at IS NULL OR o.created_at>=since_at)
   AND (before_at IS NULL OR o.created_at<before_at)
   AND (after_created IS NULL OR (o.created_at,o.id)<(after_created,after_id))
  ORDER BY o.created_at DESC,o.id DESC LIMIT page_size+1
 )
 SELECT coalesce(jsonb_agg(jsonb_build_object('id',o.id,'release',o.release,'table',o.qualified_name,
  'created_at',o.created_at,'repair_of',o.repair_of,'payload_purged_at',o.payload_purged_at,
  'change_rows',CASE WHEN o.payload_purged_at IS NULL THEN (SELECT count(*) FROM gregale_repair.changes c WHERE c.operation_id=o.id) END,
  'applied_repairs',(SELECT count(*) FROM gregale_repair.plans p WHERE p.source_operation_id=o.id AND p.scope=expected_scope AND p.applied_at IS NOT NULL),
  'latest_repair',(SELECT jsonb_build_object('plan_id',p.id,'operation_id',p.repair_operation_id,'applied_at',p.applied_at)
   FROM gregale_repair.plans p WHERE p.source_operation_id=o.id AND p.scope=expected_scope AND p.applied_at IS NOT NULL
   ORDER BY p.applied_at DESC,p.id DESC LIMIT 1)) ORDER BY o.created_at DESC,o.id DESC),'[]'::jsonb)
 INTO result FROM page o;
 RETURN result;
END $$;
