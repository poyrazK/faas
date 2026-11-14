CREATE OR REPLACE FUNCTION gregale_repair.guard_row() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog SET extra_float_digits = '3' SET IntervalStyle = 'postgres' AS $$
DECLARE enrollment gregale_repair.enrollments; fields text; new_fields text; changed boolean;
BEGIN
 IF TG_OP='INSERT' THEN
  IF nullif(current_setting('gregale.repair_operation',true),'') IS NOT NULL THEN
   RAISE EXCEPTION 'repair_capture_requires_updates' USING ERRCODE='22023';
  END IF;
  NEW.gregale_row_identity:=gen_random_uuid(); NEW.gregale_row_version:=1; RETURN NEW;
 END IF;
 IF TG_OP='DELETE' THEN
  IF nullif(current_setting('gregale.repair_operation',true),'') IS NOT NULL THEN
   RAISE EXCEPTION 'repair_capture_requires_updates' USING ERRCODE='22023';
  END IF;
  RETURN OLD;
 END IF;
 IF NEW.gregale_row_identity IS DISTINCT FROM OLD.gregale_row_identity OR NEW.gregale_row_version IS DISTINCT FROM OLD.gregale_row_version THEN
  RAISE EXCEPTION 'repair_row_identity_is_managed' USING ERRCODE='22023';
 END IF;
 SELECT * INTO STRICT enrollment FROM gregale_repair.enrollments WHERE relation=TG_RELID;
 SELECT string_agg(format('($1).%I',k),',' ORDER BY k),string_agg(format('($2).%I',k),',' ORDER BY k)
 INTO fields,new_fields FROM unnest(enrollment.keys) k;
 EXECUTE format('SELECT ROW(%s)::text IS DISTINCT FROM ROW(%s)::text',fields,new_fields) INTO changed USING OLD,NEW;
 IF changed THEN RAISE EXCEPTION 'repair_key_is_immutable' USING ERRCODE='22023'; END IF;
 NEW.gregale_row_version:=OLD.gregale_row_version+1;
 RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION gregale_repair.capture_row() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog SET extra_float_digits = '3' SET IntervalStyle = 'postgres' AS $$
DECLARE op gregale_repair.operations; enrollment gregale_repair.enrollments;
 before_data jsonb; after_data jsonb; row_key jsonb; context_id text; fields text; new_fields text; changed boolean;
BEGIN
 context_id:=nullif(current_setting('gregale.repair_operation',true),'');
 IF context_id IS NULL THEN RETURN NULL; END IF;
 SELECT * INTO STRICT op FROM gregale_repair.operations WHERE id=context_id::uuid;
 SELECT * INTO STRICT enrollment FROM gregale_repair.enrollments WHERE relation=TG_RELID;
 IF op.finalized OR op.transaction_id<>pg_current_xact_id()::text OR op.enrollment_id<>enrollment.enrollment_id THEN
  RAISE EXCEPTION 'repair_capture_context_mismatch' USING ERRCODE='22023';
 END IF;
 -- Native record text retains array bounds and raw json spelling. Do not
 -- normalize unrelated values to JSONB when checking capture completeness.
 SELECT string_agg(format('($1).%I',a.attname),',' ORDER BY a.attnum),string_agg(format('($2).%I',a.attname),',' ORDER BY a.attnum) INTO fields,new_fields
 FROM pg_attribute a WHERE a.attrelid=TG_RELID AND a.attnum>0 AND NOT a.attisdropped
  AND a.attname<>ALL(enrollment.fields||ARRAY['gregale_row_version']);
 EXECUTE format('SELECT ROW(%s)::text IS DISTINCT FROM ROW(%s)::text',fields,new_fields) INTO changed USING OLD,NEW;
 IF changed THEN RAISE EXCEPTION 'repair_capture_undeclared_field' USING ERRCODE='22023'; END IF;
 -- Convert only qualified fields and keys, under a lossless float output mode.
 SELECT string_agg(format('%L,($1).%I',k,k),',' ORDER BY k) INTO fields FROM unnest(enrollment.fields) k;
 EXECUTE format('SELECT jsonb_build_object(%s)',fields) INTO before_data USING OLD;
 EXECUTE format('SELECT jsonb_build_object(%s)',fields) INTO after_data USING NEW;
 SELECT string_agg(format('%L,($1).%I',k,k),',' ORDER BY k) INTO fields FROM unnest(enrollment.keys) k;
 EXECUTE format('SELECT jsonb_build_object(%s)',fields) INTO row_key USING OLD;
 -- Prove that every journaled native value survives JSONB reconstruction.
 -- Full float output precision alone cannot retain a negative zero sign.
 SELECT string_agg(format('($1).%I',k),',' ORDER BY k),string_agg(format('restored.%I',k),',' ORDER BY k)
 INTO fields,new_fields FROM unnest(enrollment.fields||enrollment.keys) k;
 EXECUTE format('SELECT ROW(%s)::text IS DISTINCT FROM ROW(%s)::text FROM jsonb_populate_record($1,$2) AS restored',fields,new_fields)
  INTO changed USING OLD,before_data||row_key;
 IF changed THEN RAISE EXCEPTION 'repair_capture_value_not_lossless' USING ERRCODE='22023'; END IF;
 EXECUTE format('SELECT ROW(%s)::text IS DISTINCT FROM ROW(%s)::text FROM jsonb_populate_record($1,$2) AS restored',fields,new_fields)
  INTO changed USING NEW,after_data||row_key;
 IF changed THEN RAISE EXCEPTION 'repair_capture_value_not_lossless' USING ERRCODE='22023'; END IF;
 INSERT INTO gregale_repair.changes VALUES(op.id,OLD.gregale_row_identity,row_key,before_data,after_data,OLD.gregale_row_version,NEW.gregale_row_version)
 ON CONFLICT(operation_id,row_identity) DO UPDATE SET after_values=excluded.after_values,after_version=excluded.after_version;
 IF (SELECT count(*)>__MAX_ROWS__ OR coalesce(sum(octet_length(before_values::text)+octet_length(after_values::text)+octet_length(key::text)),0)>__MAX_BYTES__ FROM gregale_repair.changes WHERE operation_id=op.id) THEN
  RAISE EXCEPTION 'repair_capture_limit' USING ERRCODE='54000';
 END IF;
 RETURN NULL;
END $$;
