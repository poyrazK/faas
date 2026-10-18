-- Owner-installed, metadata-only navigation over retained plans. Selection is
-- bounded before extracting counts/lineage; no items, journals or live rows are
-- read, and history confers no approval or permission to retry an apply.
CREATE INDEX IF NOT EXISTS gregale_repair_plan_history
 ON gregale_repair.plans(source_operation_id,scope,created_at DESC,id DESC);

CREATE OR REPLACE FUNCTION gregale_repair.list_history(source uuid, expected_scope jsonb,
 after_created timestamptz, after_id uuid, page_size integer) RETURNS jsonb
LANGUAGE plpgsql STABLE SET search_path = pg_catalog AS $$
DECLARE result jsonb;
BEGIN
 IF source IS NULL OR source='00000000-0000-0000-0000-000000000000'::uuid OR
  expected_scope IS NULL OR jsonb_typeof(expected_scope)<>'object' OR
  NOT(expected_scope ?& ARRAY['account_id','app_id','environment']) OR
  expected_scope-'account_id'-'app_id'-'environment'-'platform_tenant_id'<>'{}'::jsonb OR
  EXISTS(SELECT 1 FROM jsonb_each(expected_scope) kv WHERE jsonb_typeof(kv.value)<>'string' OR length(kv.value#>>'{}') NOT BETWEEN 1 AND 256) OR
  page_size IS NULL OR page_size NOT BETWEEN 1 AND __SOURCE_MAX_PAGE__ OR
  (after_created IS NULL)<>(after_id IS NULL) THEN
  RAISE EXCEPTION 'repair_invalid_history_query' USING ERRCODE='22023';
 END IF;
 PERFORM 1 FROM gregale_repair.operations WHERE id=source AND scope=expected_scope AND finalized;
 IF NOT FOUND THEN RAISE EXCEPTION 'repair_source_not_found' USING ERRCODE='P0002'; END IF;
 WITH page AS MATERIALIZED (
  SELECT p.* FROM gregale_repair.plans p
  WHERE p.source_operation_id=source AND p.scope=expected_scope
   AND (after_created IS NULL OR (p.created_at,p.id)<(after_created,after_id))
  ORDER BY p.created_at DESC,p.id DESC LIMIT page_size+1
 )
 SELECT coalesce(jsonb_agg(jsonb_build_object(
  'id',p.id,'digest',p.digest,'created_at',p.created_at,'expires_at',p.expires_at,
  'candidates',p.document->'candidates','conflicts',p.document->'conflicts','unavailable',p.document->'unavailable',
  'rehearsed_at',p.rehearsed_at,'approved_at',p.approved_at,'approved_by',p.approved_by,
  'applied_at',p.applied_at,'repair_operation_id',p.repair_operation_id,
  'payload_purged_at',p.payload_purged_at,'replacement_plan_id',p.replacement_plan_id,
  'replacement',CASE WHEN p.document->'replacement' IS NOT NULL THEN jsonb_build_object(
   'parent_plan_id',p.document->'replacement'->'parent_plan_id',
   'parent_plan_digest',p.document->'replacement'->'parent_plan_digest',
   'previous_candidates',p.document->'replacement'->'previous_candidates',
   'retained_candidates',p.document->'replacement'->'retained_candidates',
   'removed_candidates',p.document->'replacement'->'removed_candidates',
   'previous_excluded',p.document->'replacement'->'previous_excluded') END,
  'apply_resolution',CASE WHEN p.apply_resolution IS NOT NULL THEN jsonb_build_object(
   'outcome',p.apply_resolution->'outcome','plan_digest',p.apply_resolution->'plan_digest',
   'repair_operation_id',p.apply_resolution->'repair_operation_id',
   'resolved_at',p.apply_resolution->'resolved_at','resolved_by',p.apply_resolution->'resolved_by') END
 ) ORDER BY p.created_at DESC,p.id DESC),'[]'::jsonb) INTO result FROM page p;
 RETURN jsonb_build_object('scope',expected_scope,'source_operation_id',source,
  'checked_at',statement_timestamp(),'items',result);
END $$;
