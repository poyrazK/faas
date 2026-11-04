-- ADR-20261016: installed explicitly by the application database owner.
-- Dynamic identifiers originate only in the owner-maintained enrollment registry.
-- The row-function definitions here are the legacy fingerprint template.
-- Schema() substitutes row_contract.sql and the reviewed compatibility fingerprint.
BEGIN;
CREATE SCHEMA gregale_repair;
REVOKE ALL ON SCHEMA gregale_repair FROM PUBLIC;

CREATE TABLE gregale_repair.enrollments (
    relation regclass PRIMARY KEY,
    qualified_name text NOT NULL UNIQUE,
    enrollment_id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    fields text[] NOT NULL CHECK (cardinality(fields) > 0),
    keys text[] NOT NULL CHECK (cardinality(keys) > 0),
    fingerprint text NOT NULL,
    fingerprint_timezone text CHECK (octet_length(fingerprint_timezone) BETWEEN 1 AND 256),
    fingerprint_datestyle text CHECK (octet_length(fingerprint_datestyle) BETWEEN 1 AND 256),
    fingerprint_intervalstyle text CHECK (fingerprint_intervalstyle IN ('postgres','postgres_verbose','sql_standard','iso_8601'))
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

__CAPTURE_CONTRACT__

CREATE CONSTRAINT TRIGGER gregale_repair_finalized AFTER INSERT ON gregale_repair.operations
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION gregale_repair.require_finalized();

__RETENTION_SCHEMA__

-- Database roles are explicitly trusted application/repair roles. No public RPC
-- or arbitrary SQL caller receives capture, evidence, approval or apply authority.
REVOKE ALL ON ALL TABLES IN SCHEMA gregale_repair FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA gregale_repair FROM PUBLIC;
COMMIT;
