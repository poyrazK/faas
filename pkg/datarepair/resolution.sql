-- Outcome resolution is a source-owned, irreversible fence outside the frozen
-- plan document. The same row lock as apply is held through the closing COMMIT.
ALTER TABLE gregale_repair.plans ADD COLUMN IF NOT EXISTS apply_resolution jsonb;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='gregale_repair.plans'::regclass AND conname='gregale_repair_apply_resolution') THEN
  ALTER TABLE gregale_repair.plans ADD CONSTRAINT gregale_repair_apply_resolution CHECK (
   apply_resolution IS NULL OR (jsonb_typeof(apply_resolution)='object' AND
    apply_resolution->>'outcome'='not_committed' AND apply_resolution->>'plan_digest'=digest AND
    jsonb_typeof(apply_resolution->'repair_operation_id')='string' AND
    jsonb_typeof(apply_resolution->'resolved_at')='string' AND
    jsonb_typeof(apply_resolution->'resolved_by')='string' AND
    octet_length(apply_resolution->>'resolved_by') BETWEEN 1 AND __ACTOR_MAX_BYTES__ AND
    applied_at IS NULL AND repair_operation_id IS NULL) IS TRUE);
 END IF;
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.resolve_apply(plan uuid, expected_scope jsonb, expected_digest text, repair_operation uuid, actor text) RETURNS jsonb
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE p gregale_repair.plans;
BEGIN
 IF repair_operation IS NULL OR repair_operation='00000000-0000-0000-0000-000000000000'::uuid OR
  actor IS NULL OR octet_length(actor) NOT BETWEEN 1 AND __ACTOR_MAX_BYTES__ THEN
  RAISE EXCEPTION 'repair_invalid_resolution' USING ERRCODE='22023'; END IF;
 PERFORM pg_advisory_xact_lock_shared(hashtextextended(expected_scope::text,161018));
 -- Busy is unresolved. Never cancel an in-flight apply or infer its outcome.
 SELECT * INTO STRICT p FROM gregale_repair.plans WHERE id=plan AND scope=expected_scope FOR UPDATE NOWAIT;
 IF p.digest IS DISTINCT FROM expected_digest OR coalesce(p.retained_digest,p.digest) IS DISTINCT FROM
  encode(sha256(convert_to(p.document::text,'UTF8')),'hex') THEN
  RAISE EXCEPTION 'repair_plan_digest_mismatch' USING ERRCODE='40001'; END IF;
 IF p.applied_at IS NOT NULL THEN
  IF p.repair_operation_id IS DISTINCT FROM repair_operation THEN
   RAISE EXCEPTION 'repair_already_applied' USING ERRCODE='40001'; END IF;
  RETURN gregale_repair.get_plan(plan,expected_scope);
 END IF;
 IF p.apply_resolution IS NOT NULL THEN
  IF p.apply_resolution->>'repair_operation_id' IS DISTINCT FROM repair_operation::text THEN
   RAISE EXCEPTION 'repair_resolution_identity_mismatch' USING ERRCODE='40001'; END IF;
  RETURN gregale_repair.get_plan(plan,expected_scope);
 END IF;
 IF p.replacement_plan_id IS NOT NULL OR repair_operation=p.source_operation_id THEN
  RAISE EXCEPTION 'repair_resolution_conflict' USING ERRCODE='40001'; END IF;
 IF p.payload_purged_at IS NOT NULL THEN RAISE EXCEPTION 'repair_payload_purged' USING ERRCODE='55000'; END IF;
 UPDATE gregale_repair.plans SET apply_resolution=jsonb_build_object('outcome','not_committed',
  'plan_digest',expected_digest,'repair_operation_id',repair_operation,'resolved_at',clock_timestamp(),'resolved_by',actor) WHERE id=plan;
 RETURN gregale_repair.get_plan(plan,expected_scope);
END $$;
