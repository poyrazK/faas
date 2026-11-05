-- Explicit owner upgrade; the reviewed row-contract versions retain canonical fingerprints.
ALTER TABLE gregale_repair.operations ADD COLUMN IF NOT EXISTS capture_relation_filenode oid;
ALTER TABLE gregale_repair.operations ADD COLUMN IF NOT EXISTS capture_contract_version integer;
ALTER TABLE gregale_repair.operations DROP CONSTRAINT IF EXISTS operations_capture_contract_version_check;
ALTER TABLE gregale_repair.operations ADD CONSTRAINT operations_capture_contract_version_check CHECK(capture_contract_version IN (1,2,3));

CREATE OR REPLACE FUNCTION gregale_repair.enroll(target regclass, selected_fields text[]) RETURNS uuid
LANGUAGE plpgsql SET search_path = pg_catalog SET jit = 'off' AS $$
DECLARE qualified text; pk text[]; field_name text; enrollment uuid;
BEGIN
 IF current_setting('server_version_num')::int NOT BETWEEN 160000 AND 169999 THEN RAISE EXCEPTION 'repair_postgres16_required' USING ERRCODE='22023'; END IF;
 IF NOT (__CURRENT_ROW_CONTRACT__) THEN RAISE EXCEPTION 'repair_capture_contract_changed' USING ERRCODE='22023'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=target AND c.relkind='r' AND NOT c.relispartition AND n.nspname NOT IN ('pg_catalog','information_schema','gregale_repair') AND n.nspname NOT LIKE 'pg_%' AND NOT c.relrowsecurity) OR EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=target OR inhparent=target) THEN
  RAISE EXCEPTION 'repair_requires_regular_owner_scoped_table' USING ERRCODE='22023';
 END IF;
 EXECUTE format('LOCK TABLE %s IN ACCESS EXCLUSIVE MODE',target);
 IF EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=target AND attnum>0 AND NOT attisdropped AND attgenerated<>'') THEN
  RAISE EXCEPTION 'repair_generated_columns_unsupported' USING ERRCODE='22023';
 END IF;
 IF EXISTS(SELECT 1 FROM gregale_repair.enrollments WHERE relation=target) THEN RAISE EXCEPTION 'repair_already_enrolled' USING ERRCODE='22023'; END IF;
 IF cardinality(selected_fields) IS NULL OR cardinality(selected_fields)<1 OR cardinality(selected_fields)<>(SELECT count(DISTINCT f) FROM unnest(selected_fields) f) THEN
  RAISE EXCEPTION 'repair_invalid_fields' USING ERRCODE='22023';
 END IF;
 IF EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=target AND NOT tgisinternal) THEN RAISE EXCEPTION 'repair_custom_triggers_unsupported' USING ERRCODE='22023'; END IF;
 IF EXISTS(SELECT 1 FROM pg_rewrite WHERE ev_class=target) THEN RAISE EXCEPTION 'repair_rules_unsupported' USING ERRCODE='22023'; END IF;
 IF __CASCADING_TARGET_FIELDS__ THEN
  RAISE EXCEPTION 'repair_cascading_fields_unsupported' USING ERRCODE='22023';
 END IF;
 IF __UNSUPPORTED_TARGET_EXPRESSIONS__ THEN
  RAISE EXCEPTION 'repair_custom_expressions_unsupported' USING ERRCODE='22023';
 END IF;
 SELECT array_agg(a.attname ORDER BY k.ord) INTO pk FROM pg_index i CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY k(num,ord)
 JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.num WHERE i.indrelid=target AND i.indisprimary AND k.ord<=i.indnkeyatts;
 IF pk IS NULL THEN RAISE EXCEPTION 'repair_primary_key_required' USING ERRCODE='22023'; END IF;
 IF __LOSSY_TARGET_TYPES__ THEN RAISE EXCEPTION 'repair_lossy_field_types_unsupported' USING ERRCODE='22023'; END IF;
 FOREACH field_name IN ARRAY selected_fields LOOP
  IF field_name=ANY(pk) OR field_name IN ('gregale_row_identity','gregale_row_version') OR NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=target AND attname=field_name AND attnum>0 AND NOT attisdropped AND attgenerated='' AND attidentity='') THEN
   RAISE EXCEPTION 'repair_invalid_field' USING ERRCODE='22023';
  END IF;
 END LOOP;
 SELECT format('%I.%I',n.nspname,c.relname) INTO qualified FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=target;
 EXECUTE format('ALTER TABLE %s ADD COLUMN gregale_row_identity uuid NOT NULL DEFAULT gen_random_uuid(), ADD COLUMN gregale_row_version bigint NOT NULL DEFAULT 1 CHECK(gregale_row_version>0)',target);
 EXECUTE format('CREATE UNIQUE INDEX ON %s (gregale_row_identity)',target);
 INSERT INTO gregale_repair.enrollments(relation,qualified_name,fields,keys,fingerprint,fingerprint_timezone,fingerprint_datestyle,fingerprint_intervalstyle) VALUES(target,qualified,selected_fields,pk,'pending','UTC','ISO, YMD','postgres') RETURNING enrollment_id INTO enrollment;
 EXECUTE format('CREATE TRIGGER gregale_repair_guard BEFORE INSERT OR UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION gregale_repair.guard_row()',target);
 EXECUTE format('CREATE TRIGGER gregale_repair_capture AFTER UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION gregale_repair.capture_row()',target);
 UPDATE gregale_repair.enrollments SET fingerprint=gregale_repair.fingerprint(target),type_fingerprint=gregale_repair.type_fingerprint(target,false) WHERE relation=target;
 RETURN enrollment;
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.check_enrollment(enrollment uuid) RETURNS gregale_repair.enrollments
LANGUAGE plpgsql SET search_path = pg_catalog SET jit = 'off' AS $$
DECLARE e gregale_repair.enrollments;
BEGIN
 IF current_setting('session_replication_role')<>'origin' THEN
  RAISE EXCEPTION 'repair_requires_origin_session' USING ERRCODE='22023';
 END IF;
 IF NOT (__CURRENT_ROW_CONTRACT__) THEN RAISE EXCEPTION 'repair_capture_contract_changed' USING ERRCODE='22023'; END IF;
 SELECT * INTO STRICT e FROM gregale_repair.enrollments WHERE enrollment_id=enrollment;
 IF e.fingerprint_datestyle IS NULL OR e.fingerprint_intervalstyle IS NULL THEN RAISE EXCEPTION 'repair_fingerprint_rendering_requires_review' USING ERRCODE='22023'; END IF;
 IF e.fingerprint_timezone IS NULL THEN RAISE EXCEPTION 'repair_fingerprint_timezone_requires_review' USING ERRCODE='22023'; END IF;
 IF current_setting('transaction_isolation')='read committed' THEN
  -- Concurrent captured writers share this lock; ordinary schema DDL cannot
  -- pass it. A concurrent index build started later must wait for this writer
  -- before building/activating its index. Refuse maintenance already running.
  EXECUTE format('LOCK TABLE %s IN ROW EXCLUSIVE MODE',e.relation);
  IF NOT EXISTS(SELECT 1 FROM gregale_repair.operations
   WHERE id=nullif(current_setting('gregale.repair_guarded_'||e.relation::oid::text,true),'')::uuid
    AND enrollment_id=e.enrollment_id AND transaction_id=pg_current_xact_id()::text) AND
   EXISTS(SELECT 1 FROM pg_locks WHERE locktype='relation' AND relation=e.relation AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
    AND mode='ShareUpdateExclusiveLock' AND pid IS DISTINCT FROM pg_backend_pid()) THEN
   RAISE EXCEPTION 'repair_schema_maintenance_busy' USING ERRCODE='40001';
  END IF;
 ELSE
  -- Preserve the stronger fence for callers retaining an older catalog snapshot.
  EXECUTE format('LOCK TABLE %s IN SHARE UPDATE EXCLUSIVE MODE',e.relation);
 END IF;
 IF EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=e.relation AND attnum>0 AND NOT attisdropped AND attgenerated<>'') THEN
  RAISE EXCEPTION 'repair_generated_columns_unsupported' USING ERRCODE='22023';
 END IF;
 IF __CASCADING_ENROLLED_FIELDS__ THEN RAISE EXCEPTION 'repair_cascading_fields_unsupported' USING ERRCODE='22023'; END IF;
 IF __TYPE_CONTRACT_CHANGED__ THEN RAISE EXCEPTION 'repair_schema_changed' USING ERRCODE='40001'; END IF;
 IF __UNSUPPORTED_ENROLLED_EXPRESSIONS__ THEN
  RAISE EXCEPTION 'repair_custom_expressions_unsupported' USING ERRCODE='22023';
 END IF;
 IF __LOSSY_ENROLLED_TYPES__ THEN RAISE EXCEPTION 'repair_lossy_field_types_unsupported' USING ERRCODE='22023'; END IF;
 IF e.fingerprint IS DISTINCT FROM gregale_repair.fingerprint(e.relation) OR EXISTS(SELECT 1 FROM pg_class WHERE oid=e.relation AND relrowsecurity) THEN
  RAISE EXCEPTION 'repair_schema_changed' USING ERRCODE='40001';
 END IF;
 RETURN e;
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.finish_operation(operation uuid) RETURNS void
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE captured_filenode oid; target regclass; captured_types text; captured_definition text; enrollment uuid;
BEGIN
 IF current_setting('session_replication_role')<>'origin' THEN
  RAISE EXCEPTION 'repair_requires_origin_session' USING ERRCODE='22023';
 END IF;
 IF nullif(current_setting('gregale.repair_operation',true),'') IS DISTINCT FROM operation::text THEN RAISE EXCEPTION 'repair_capture_context_mismatch' USING ERRCODE='22023'; END IF;
 SELECT o.capture_relation_filenode,e.relation,o.capture_type_state,o.type_fingerprint,e.enrollment_id INTO captured_filenode,target,captured_types,captured_definition,enrollment
 FROM gregale_repair.operations o JOIN gregale_repair.enrollments e ON e.enrollment_id=o.enrollment_id
 WHERE o.id=operation AND NOT o.finalized AND o.transaction_id=pg_current_xact_id()::text;
 IF NOT FOUND THEN RAISE EXCEPTION 'repair_capture_context_mismatch' USING ERRCODE='22023'; END IF;
 -- TRUNCATE does not fire row triggers. Its transactional storage rewrite must
 -- abort capture, including when no UPDATE produced a journal row.
 IF captured_filenode IS DISTINCT FROM pg_relation_filenode(target) THEN
  RAISE EXCEPTION 'repair_capture_requires_updates' USING ERRCODE='22023';
 END IF;
 PERFORM gregale_repair.check_enrollment(enrollment);
 IF captured_types IS DISTINCT FROM gregale_repair.type_fingerprint(target,true) OR captured_definition IS DISTINCT FROM gregale_repair.type_fingerprint(target,false) THEN
  RAISE EXCEPTION 'repair_schema_changed' USING ERRCODE='40001';
 END IF;
 UPDATE gregale_repair.operations SET finalized=true WHERE id=operation AND NOT finalized AND transaction_id=pg_current_xact_id()::text;
 IF NOT FOUND THEN RAISE EXCEPTION 'repair_capture_context_mismatch' USING ERRCODE='22023'; END IF;
 PERFORM set_config('gregale.repair_operation','',true);
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.require_finalized() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE o gregale_repair.operations; target regclass;
BEGIN
 SELECT * INTO STRICT o FROM gregale_repair.operations WHERE id=NEW.id;
 IF NOT o.finalized THEN
  RAISE EXCEPTION 'repair_capture_not_finalized' USING ERRCODE='22023';
 END IF;
 SELECT relation INTO STRICT target FROM gregale_repair.enrollments WHERE enrollment_id=o.enrollment_id;
 -- Also fence a rewrite after finish_operation, before the enclosing COMMIT.
 IF o.capture_relation_filenode IS DISTINCT FROM pg_relation_filenode(target) THEN
  RAISE EXCEPTION 'repair_capture_requires_updates' USING ERRCODE='22023';
 END IF;
 PERFORM gregale_repair.check_enrollment(o.enrollment_id);
 IF o.capture_type_state IS DISTINCT FROM gregale_repair.type_fingerprint(target,true) OR o.type_fingerprint IS DISTINCT FROM gregale_repair.type_fingerprint(target,false) THEN
  RAISE EXCEPTION 'repair_schema_changed' USING ERRCODE='40001';
 END IF;
 RETURN NULL;
END $$;

-- Correct only the known legacy INCLUDE-column classification. Captured keys
-- remain frozen presentation evidence; row UUIDs and versions govern apply.
DO $$
DECLARE e gregale_repair.enrollments; key_columns text[]; index_columns text[];
BEGIN
 FOR e IN SELECT * FROM gregale_repair.enrollments ORDER BY relation::oid LOOP
  SELECT array_agg(a.attname ORDER BY k.ord) FILTER(WHERE k.ord<=i.indnkeyatts),
   array_agg(a.attname ORDER BY k.ord) INTO key_columns,index_columns
  FROM pg_index i CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY k(num,ord)
  JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.num
  WHERE i.indrelid=e.relation AND i.indisprimary;
  IF e.keys IS NOT DISTINCT FROM index_columns AND e.keys IS DISTINCT FROM key_columns THEN
   EXECUTE format('LOCK TABLE %s IN SHARE UPDATE EXCLUSIVE MODE',e.relation);
   IF e.fingerprint IS DISTINCT FROM gregale_repair.fingerprint(e.relation) THEN
    RAISE EXCEPTION 'repair_schema_changed' USING ERRCODE='40001';
   END IF;
   UPDATE gregale_repair.enrollments SET keys=key_columns WHERE enrollment_id=e.enrollment_id;
  END IF;
 END LOOP;
END $$;
