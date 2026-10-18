-- ADR-20261016: installed explicitly by the application database owner.
-- Dynamic identifiers originate only in the owner-maintained enrollment registry.
BEGIN;
CREATE SCHEMA gregale_repair;
REVOKE ALL ON SCHEMA gregale_repair FROM PUBLIC;

CREATE TABLE gregale_repair.enrollments (
    relation regclass PRIMARY KEY,
    qualified_name text NOT NULL UNIQUE,
    enrollment_id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    fields text[] NOT NULL CHECK (cardinality(fields) > 0),
    keys text[] NOT NULL CHECK (cardinality(keys) > 0),
    fingerprint text NOT NULL
);
CREATE TABLE gregale_repair.operations (
    id uuid PRIMARY KEY CHECK (id <> '00000000-0000-0000-0000-000000000000'),
    scope jsonb NOT NULL CHECK (jsonb_typeof(scope) = 'object'),
    release text NOT NULL CHECK (length(release) BETWEEN 1 AND 256),
    enrollment_id uuid NOT NULL REFERENCES gregale_repair.enrollments(enrollment_id),
    schema_fingerprint text NOT NULL,
    transaction_id text NOT NULL,
    finalized boolean NOT NULL DEFAULT false,
    repair_of uuid REFERENCES gregale_repair.operations(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE gregale_repair.changes (
    operation_id uuid NOT NULL REFERENCES gregale_repair.operations(id),
    row_identity uuid NOT NULL,
    key jsonb NOT NULL CHECK (jsonb_typeof(key) = 'object'),
    before_values jsonb NOT NULL CHECK (jsonb_typeof(before_values) = 'object'),
    after_values jsonb NOT NULL CHECK (jsonb_typeof(after_values) = 'object'),
    before_version bigint NOT NULL CHECK (before_version > 0),
    after_version bigint NOT NULL CHECK (after_version > before_version),
    PRIMARY KEY (operation_id, row_identity)
);
CREATE TABLE gregale_repair.plans (
    id uuid PRIMARY KEY,
    source_operation_id uuid NOT NULL REFERENCES gregale_repair.operations(id),
    scope jsonb NOT NULL CHECK (jsonb_typeof(scope) = 'object'),
    document jsonb NOT NULL CHECK (jsonb_typeof(document) = 'object'),
    digest text NOT NULL CHECK (length(digest) = 64),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    rehearsed_at timestamptz,
    rehearsal jsonb CHECK (jsonb_typeof(rehearsal) = 'object'),
    approved_at timestamptz,
    approved_by text CHECK (length(approved_by) BETWEEN 1 AND 256),
    applied_at timestamptz,
    repair_operation_id uuid UNIQUE REFERENCES gregale_repair.operations(id),
    CHECK ((approved_at IS NULL) = (approved_by IS NULL)),
    CHECK ((applied_at IS NULL) = (repair_operation_id IS NULL))
);

CREATE FUNCTION gregale_repair.require_finalized() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM gregale_repair.operations WHERE id=NEW.id AND NOT finalized) THEN
  RAISE EXCEPTION 'repair_capture_not_finalized' USING ERRCODE='22023';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER gregale_repair_finalized AFTER INSERT ON gregale_repair.operations
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION gregale_repair.require_finalized();

CREATE FUNCTION gregale_repair.fingerprint(target regclass) RETURNS text
LANGUAGE sql STABLE SET search_path = pg_catalog AS $$
 SELECT encode(sha256(convert_to(jsonb_build_object(
  'table',(SELECT jsonb_build_array(relkind,relispartition,relrowsecurity,relforcerowsecurity) FROM pg_class WHERE oid=target),
  'inheritance',(SELECT jsonb_agg(jsonb_build_array(inhrelid::regclass::text,inhparent::regclass::text) ORDER BY inhrelid,inhparent) FROM pg_inherits WHERE inhrelid=target OR inhparent=target),
  'rules',(SELECT jsonb_agg(pg_get_ruledef(oid) ORDER BY rulename) FROM pg_rewrite WHERE ev_class=target),
  'columns',(SELECT jsonb_agg(jsonb_build_array(a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull,a.attgenerated,pg_get_expr(d.adbin,d.adrelid)) ORDER BY a.attnum)
    FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid=target AND a.attnum>0 AND NOT a.attisdropped),
  'constraints',(SELECT jsonb_agg(pg_get_constraintdef(oid) ORDER BY conname) FROM pg_constraint WHERE conrelid=target),
  'triggers',(SELECT jsonb_agg(jsonb_build_array(t.tgname,t.tgenabled,pg_get_triggerdef(t.oid),p.prosrc) ORDER BY t.tgname)
    FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.tgrelid=target AND NOT t.tgisinternal)
 )::text,'UTF8')),'hex')
$$;

CREATE FUNCTION gregale_repair.guard_row() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE enrollment gregale_repair.enrollments; old_key jsonb; new_key jsonb;
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
 SELECT jsonb_object_agg(k,to_jsonb(OLD)->k),jsonb_object_agg(k,to_jsonb(NEW)->k) INTO old_key,new_key FROM unnest(enrollment.keys) k;
 IF old_key IS DISTINCT FROM new_key THEN RAISE EXCEPTION 'repair_key_is_immutable' USING ERRCODE='22023'; END IF;
 NEW.gregale_row_version:=OLD.gregale_row_version+1;
 RETURN NEW;
END $$;

CREATE FUNCTION gregale_repair.capture_row() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE op gregale_repair.operations; enrollment gregale_repair.enrollments;
 before_data jsonb; after_data jsonb; row_key jsonb; context_id text;
BEGIN
 context_id:=nullif(current_setting('gregale.repair_operation',true),'');
 IF context_id IS NULL THEN RETURN NULL; END IF;
 SELECT * INTO STRICT op FROM gregale_repair.operations WHERE id=context_id::uuid;
 SELECT * INTO STRICT enrollment FROM gregale_repair.enrollments WHERE relation=TG_RELID;
 IF op.finalized OR op.transaction_id<>pg_current_xact_id()::text OR op.enrollment_id<>enrollment.enrollment_id THEN
  RAISE EXCEPTION 'repair_capture_context_mismatch' USING ERRCODE='22023';
 END IF;
 IF (to_jsonb(OLD)-enrollment.fields-ARRAY['gregale_row_version']) IS DISTINCT FROM (to_jsonb(NEW)-enrollment.fields-ARRAY['gregale_row_version']) THEN
  RAISE EXCEPTION 'repair_capture_undeclared_field' USING ERRCODE='22023';
 END IF;
 SELECT jsonb_object_agg(k,to_jsonb(OLD)->k),jsonb_object_agg(k,to_jsonb(NEW)->k) INTO before_data,after_data FROM unnest(enrollment.fields) k;
 SELECT jsonb_object_agg(k,to_jsonb(OLD)->k) INTO row_key FROM unnest(enrollment.keys) k;
 INSERT INTO gregale_repair.changes VALUES(op.id,OLD.gregale_row_identity,row_key,before_data,after_data,OLD.gregale_row_version,NEW.gregale_row_version)
 ON CONFLICT(operation_id,row_identity) DO UPDATE SET after_values=excluded.after_values,after_version=excluded.after_version;
 IF (SELECT count(*)>__MAX_ROWS__ OR coalesce(sum(octet_length(before_values::text)+octet_length(after_values::text)+octet_length(key::text)),0)>__MAX_BYTES__ FROM gregale_repair.changes WHERE operation_id=op.id) THEN
  RAISE EXCEPTION 'repair_capture_limit' USING ERRCODE='54000';
 END IF;
 RETURN NULL;
END $$;

CREATE FUNCTION gregale_repair.enroll(target regclass, selected_fields text[]) RETURNS uuid
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE qualified text; pk text[]; field_name text; enrollment uuid;
BEGIN
 IF current_setting('server_version_num')::int NOT BETWEEN 160000 AND 169999 THEN RAISE EXCEPTION 'repair_postgres16_required' USING ERRCODE='22023'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=target AND c.relkind='r' AND NOT c.relispartition AND n.nspname NOT IN ('pg_catalog','information_schema','gregale_repair') AND n.nspname NOT LIKE 'pg_%' AND NOT c.relrowsecurity) OR EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=target OR inhparent=target) THEN
  RAISE EXCEPTION 'repair_requires_regular_owner_scoped_table' USING ERRCODE='22023';
 END IF;
 EXECUTE format('LOCK TABLE %s IN ACCESS EXCLUSIVE MODE',target);
 IF EXISTS(SELECT 1 FROM gregale_repair.enrollments WHERE relation=target) THEN RAISE EXCEPTION 'repair_already_enrolled' USING ERRCODE='22023'; END IF;
 IF cardinality(selected_fields) IS NULL OR cardinality(selected_fields)<1 OR cardinality(selected_fields)<>(SELECT count(DISTINCT f) FROM unnest(selected_fields) f) THEN
  RAISE EXCEPTION 'repair_invalid_fields' USING ERRCODE='22023';
 END IF;
 IF EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=target AND NOT tgisinternal) THEN RAISE EXCEPTION 'repair_custom_triggers_unsupported' USING ERRCODE='22023'; END IF;
 IF EXISTS(SELECT 1 FROM pg_rewrite WHERE ev_class=target) THEN RAISE EXCEPTION 'repair_rules_unsupported' USING ERRCODE='22023'; END IF;
 IF EXISTS(SELECT 1 FROM pg_constraint c CROSS JOIN LATERAL unnest(c.confkey) k
  JOIN pg_attribute a ON a.attrelid=c.confrelid AND a.attnum=k WHERE c.confrelid=target AND c.confupdtype IN ('c','n','d') AND a.attname=ANY(selected_fields)) THEN
  RAISE EXCEPTION 'repair_cascading_fields_unsupported' USING ERRCODE='22023';
 END IF;
 IF EXISTS(SELECT 1 FROM pg_depend d JOIN pg_proc p ON p.oid=d.refobjid JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE d.refclassid='pg_proc'::regclass AND (n.nspname<>'pg_catalog' OR p.provolatile<>'i') AND
   ((d.classid='pg_constraint'::regclass AND d.objid IN(SELECT oid FROM pg_constraint WHERE conrelid=target)) OR
    (d.classid='pg_attrdef'::regclass AND d.objid IN(SELECT oid FROM pg_attrdef WHERE adrelid=target)))) THEN
  RAISE EXCEPTION 'repair_custom_expressions_unsupported' USING ERRCODE='22023';
 END IF;
 SELECT array_agg(a.attname ORDER BY k.ord) INTO pk FROM pg_index i CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY k(num,ord)
 JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.num WHERE i.indrelid=target AND i.indisprimary;
 IF pk IS NULL THEN RAISE EXCEPTION 'repair_primary_key_required' USING ERRCODE='22023'; END IF;
 FOREACH field_name IN ARRAY selected_fields LOOP
  IF field_name=ANY(pk) OR field_name IN ('gregale_row_identity','gregale_row_version') OR NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=target AND attname=field_name AND attnum>0 AND NOT attisdropped AND attgenerated='' AND attidentity='') THEN
   RAISE EXCEPTION 'repair_invalid_field' USING ERRCODE='22023';
  END IF;
 END LOOP;
 SELECT format('%I.%I',n.nspname,c.relname) INTO qualified FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=target;
 EXECUTE format('ALTER TABLE %s ADD COLUMN gregale_row_identity uuid NOT NULL DEFAULT gen_random_uuid(), ADD COLUMN gregale_row_version bigint NOT NULL DEFAULT 1 CHECK(gregale_row_version>0)',target);
 EXECUTE format('CREATE UNIQUE INDEX ON %s (gregale_row_identity)',target);
 INSERT INTO gregale_repair.enrollments(relation,qualified_name,fields,keys,fingerprint) VALUES(target,qualified,selected_fields,pk,'pending') RETURNING enrollment_id INTO enrollment;
 EXECUTE format('CREATE TRIGGER gregale_repair_guard BEFORE INSERT OR UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION gregale_repair.guard_row()',target);
 EXECUTE format('CREATE TRIGGER gregale_repair_capture AFTER UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION gregale_repair.capture_row()',target);
 UPDATE gregale_repair.enrollments SET fingerprint=gregale_repair.fingerprint(target) WHERE relation=target;
 RETURN enrollment;
END $$;

CREATE FUNCTION gregale_repair.check_enrollment(enrollment uuid) RETURNS gregale_repair.enrollments
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE e gregale_repair.enrollments;
BEGIN
 SELECT * INTO STRICT e FROM gregale_repair.enrollments WHERE enrollment_id=enrollment;
 -- Ordinary row writers remain concurrent. This also fences inheritance,
 -- trigger/rule changes and concurrent index DDL for the bounded transaction.
 EXECUTE format('LOCK TABLE %s IN SHARE UPDATE EXCLUSIVE MODE',e.relation);
 IF e.fingerprint IS DISTINCT FROM gregale_repair.fingerprint(e.relation) OR EXISTS(SELECT 1 FROM pg_class WHERE oid=e.relation AND relrowsecurity) THEN
  RAISE EXCEPTION 'repair_schema_changed' USING ERRCODE='40001';
 END IF;
 RETURN e;
END $$;

CREATE FUNCTION gregale_repair.finish_operation(operation uuid) RETURNS void
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
 IF nullif(current_setting('gregale.repair_operation',true),'') IS DISTINCT FROM operation::text THEN RAISE EXCEPTION 'repair_capture_context_mismatch' USING ERRCODE='22023'; END IF;
 UPDATE gregale_repair.operations SET finalized=true WHERE id=operation AND NOT finalized AND transaction_id=pg_current_xact_id()::text;
 IF NOT FOUND THEN RAISE EXCEPTION 'repair_capture_context_mismatch' USING ERRCODE='22023'; END IF;
 PERFORM set_config('gregale.repair_operation','',true);
END $$;

__RETENTION_SCHEMA__

-- Database roles are explicitly trusted application/repair roles. No public RPC
-- or arbitrary SQL caller receives capture, evidence, approval or apply authority.
REVOKE ALL ON ALL TABLES IN SCHEMA gregale_repair FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA gregale_repair FROM PUBLIC;
COMMIT;
