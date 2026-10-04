-- ADR-375: bounded additional attempts preserve the original verification journal.
-- Every row links the exact predecessor closure; no original row is reopened.
-- name: CopyDatabaseVerificationRetrySchemaExists :one
SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname='gregale_copy_database_verification_retries')::boolean;

-- name: InstallCopyDatabaseVerificationRetrySchema :exec
CREATE SCHEMA gregale_copy_database_verification_retries;

-- name: InstallCopyDatabaseVerificationRetryWindows :exec
CREATE TABLE gregale_copy_database_verification_retries.windows (
 source_oid oid NOT NULL CHECK (source_oid<>0),
 target_oid oid NOT NULL CHECK (target_oid<>0),
 owner_id uuid NOT NULL UNIQUE CHECK (owner_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 plan_fingerprint text NOT NULL CHECK (plan_fingerprint ~ '^[0-9a-f]{64}$'),
 preparation_created_at timestamptz NOT NULL,
 state text NOT NULL CHECK (state IN ('open','closing','closed')),
 opened_at timestamptz NOT NULL, closed_at timestamptz,
 import_owner_id uuid NOT NULL CHECK (import_owner_id<>owner_id AND import_owner_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 import_opened_at timestamptz NOT NULL, import_closed_at timestamptz NOT NULL,
 attempt integer NOT NULL CHECK (attempt BETWEEN 2 AND 3),
 previous_owner_id uuid NOT NULL UNIQUE CHECK (previous_owner_id<>owner_id AND previous_owner_id<>import_owner_id AND previous_owner_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 previous_opened_at timestamptz NOT NULL, previous_closed_at timestamptz NOT NULL,
 PRIMARY KEY (source_oid,attempt),
 CHECK (isfinite(previous_opened_at) AND isfinite(previous_closed_at) AND previous_opened_at>=import_closed_at AND previous_closed_at>=previous_opened_at AND opened_at>=previous_closed_at),
 CHECK (isfinite(preparation_created_at) AND isfinite(opened_at) AND
  isfinite(import_opened_at) AND isfinite(import_closed_at) AND import_opened_at>=preparation_created_at AND
  import_closed_at>=import_opened_at AND opened_at>=import_closed_at AND
  ((state IN ('open','closing') AND closed_at IS NULL) OR
   (state='closed' AND closed_at IS NOT NULL AND isfinite(closed_at) AND closed_at>=opened_at)))
);

-- name: InstallCopyDatabaseVerificationRetryActiveIndex :exec
CREATE UNIQUE INDEX windows_one_active ON gregale_copy_database_verification_retries.windows ((1)) WHERE state IN ('open','closing');

-- name: PrivateCopyDatabaseVerificationRetryJournal :one
SELECT EXISTS (
 SELECT 1 FROM pg_catalog.pg_namespace n WHERE n.nspname='gregale_copy_database_verification_retries'
 AND n.nspowner=(SELECT oid FROM pg_catalog.pg_roles WHERE rolname=current_user)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.aclexplode(COALESCE(n.nspacl,pg_catalog.acldefault('n',n.nspowner))) a WHERE a.grantee<>n.nspowner)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p WHERE p.pronamespace=n.oid)
 AND (SELECT count(*) FROM pg_catalog.pg_class c WHERE c.relnamespace=n.oid AND c.relkind<>'i')=1
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_class c WHERE c.relnamespace=n.oid AND c.relkind='r' AND c.relname='windows')
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_index i ON i.indexrelid=c.oid
  WHERE c.relnamespace=n.oid AND c.relname='windows_one_active' AND i.indisunique AND i.indisvalid AND i.indisready
  AND i.indrelid=(SELECT oid FROM pg_catalog.pg_class WHERE relnamespace=n.oid AND relname='windows')
  AND i.indnkeyatts=1 AND i.indnatts=1 AND i.indkey::text='0'
  AND pg_catalog.pg_get_expr(i.indexprs,i.indrelid)='1'
  AND pg_catalog.pg_get_expr(i.indpred,i.indrelid)='(state = ANY (ARRAY[''open''::text, ''closing''::text]))'
  AND c.relam=(SELECT oid FROM pg_catalog.pg_am WHERE amname='btree'))
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c WHERE c.relnamespace=n.oid AND
  (c.relowner<>n.nspowner OR c.relrowsecurity OR c.relforcerowsecurity OR
   EXISTS(SELECT 1 FROM pg_catalog.aclexplode(COALESCE(c.relacl,pg_catalog.acldefault('r',c.relowner))) a WHERE a.grantee<>c.relowner) OR
   EXISTS(SELECT 1 FROM pg_catalog.pg_trigger t WHERE t.tgrelid=c.oid) OR
   EXISTS(SELECT 1 FROM pg_catalog.pg_rewrite r WHERE r.ev_class=c.oid)))
 AND (SELECT pg_catalog.array_agg(a.attname::text ORDER BY a.attnum) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=n.oid AND c.relname='windows' AND a.attnum>0 AND NOT a.attisdropped)=ARRAY['source_oid','target_oid','owner_id','plan_fingerprint','preparation_created_at','state','opened_at','closed_at','import_owner_id','import_opened_at','import_closed_at','attempt','previous_owner_id','previous_opened_at','previous_closed_at']::text[]
 AND (SELECT pg_catalog.array_agg(a.atttypid::regtype::text ORDER BY a.attnum) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=n.oid AND c.relname='windows' AND a.attnum>0 AND NOT a.attisdropped)=ARRAY['oid','oid','uuid','text','timestamp with time zone','text','timestamp with time zone','timestamp with time zone','uuid','timestamp with time zone','timestamp with time zone','integer','uuid','timestamp with time zone','timestamp with time zone']::text[]
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=n.oid AND c.relkind='r' AND a.attnum>0 AND
  (a.attisdropped OR a.attgenerated<>'' OR (NOT a.attnotnull AND a.attname<>'closed_at')))
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_attrdef a JOIN pg_catalog.pg_class c ON c.oid=a.adrelid WHERE c.relnamespace=n.oid)
)::boolean;

-- name: CopyDatabaseVerificationRetryWindows :many
SELECT * FROM gregale_copy_database_verification_retries.windows ORDER BY source_oid,attempt;

-- name: InsertCopyDatabaseVerificationRetryWindow :exec
INSERT INTO gregale_copy_database_verification_retries.windows
(source_oid,target_oid,owner_id,plan_fingerprint,preparation_created_at,state,opened_at,import_owner_id,import_opened_at,import_closed_at,attempt,previous_owner_id,previous_opened_at,previous_closed_at)
VALUES ($1::oid,$2::oid,$3::uuid,$4::text,$5::timestamptz,'open',clock_timestamp(),$6::uuid,$7::timestamptz,$8::timestamptz,$9::integer,$10::uuid,$11::timestamptz,$12::timestamptz);

-- Functions are invoker-only in the dedicated connection's pg_temp namespace.
-- No database ACL is rewritten: normal GRANT/REVOKE cannot restore a NULL ACL.
-- Exact login/ownership capability checks therefore precede opening admission.
-- Provider administrators (superusers) are outside customer SQL admission; the
-- enclosing borrower must independently authenticate provider isolation.
-- name: InstallCopyDatabaseVerificationRetryMutation :exec
CREATE OR REPLACE FUNCTION pg_temp.gregale_copy_database_verification_retries_change(
 p_source oid,p_target oid,p_name text,p_owner oid,p_verification uuid,
 p_fingerprint text,p_prepared timestamptz,p_action text,p_template boolean,
 p_limit integer,p_verification_limit integer,p_import_owner uuid,
 p_import_opened timestamptz,p_import_closed timestamptz,
 p_attempt integer,p_previous uuid,p_previous_opened timestamptz,p_previous_closed timestamptz) RETURNS boolean
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $body$
DECLARE
 d pg_catalog.pg_database%ROWTYPE;
 w gregale_copy_database_verification_retries.windows%ROWTYPE;
 imported gregale_copy_database_maintenance.windows%ROWTYPE;
 prior_state text; prior_owner uuid; prior_opened timestamptz; prior_closed timestamptz;
BEGIN
 SELECT * INTO STRICT w FROM gregale_copy_database_verification_retries.windows WHERE source_oid=p_source AND owner_id=p_verification FOR UPDATE;
 IF w.target_oid<>p_target OR w.owner_id<>p_verification OR w.plan_fingerprint<>p_fingerprint OR w.preparation_created_at<>p_prepared OR w.attempt<>p_attempt OR
  w.previous_owner_id<>p_previous OR w.previous_opened_at<>p_previous_opened OR w.previous_closed_at<>p_previous_closed THEN
  RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='database verification ownership changed';
 END IF;
 SELECT * INTO STRICT imported FROM gregale_copy_database_maintenance.windows WHERE source_oid=p_source FOR SHARE;
 IF imported.state<>'closed' OR imported.owner_id<>p_import_owner OR imported.target_oid<>p_target OR
  imported.plan_fingerprint<>p_fingerprint OR imported.preparation_created_at<>p_prepared OR
  imported.opened_at<>p_import_opened OR imported.closed_at<>p_import_closed OR
  w.import_owner_id<>p_import_owner OR w.import_opened_at<>p_import_opened OR w.import_closed_at<>p_import_closed OR
  EXISTS(SELECT 1 FROM gregale_copy_database_maintenance.windows WHERE state<>'closed') THEN
  RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='original closed import window changed';
 END IF;
 IF p_attempt=2 THEN
  SELECT state,owner_id,opened_at,closed_at INTO STRICT prior_state,prior_owner,prior_opened,prior_closed
   FROM gregale_copy_database_verification.windows WHERE source_oid=p_source FOR SHARE;
 ELSE
  SELECT state,owner_id,opened_at,closed_at INTO STRICT prior_state,prior_owner,prior_opened,prior_closed
   FROM gregale_copy_database_verification_retries.windows WHERE source_oid=p_source AND attempt=p_attempt-1 FOR SHARE;
 END IF;
 IF prior_state<>'closed' OR prior_owner<>p_previous OR prior_opened<>p_previous_opened OR prior_closed<>p_previous_closed THEN
  RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='previous verification closure changed';
 END IF;
 SELECT * INTO STRICT d FROM pg_catalog.pg_database WHERE oid=p_target;
 IF d.datname::text<>p_name OR d.datdba<>p_owner OR d.datname=current_database() THEN
  RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='database verification identity changed';
 END IF;
 IF NOT pg_catalog.pg_has_role(current_user,p_owner,'USAGE') THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='database verification owner authority unavailable';
 END IF;
 IF p_action='open' THEN
  IF EXISTS(SELECT 1 FROM gregale_copy_database_verification.windows WHERE state<>'closed') OR
   EXISTS(SELECT 1 FROM gregale_copy_database_verification_retries.windows WHERE owner_id<>p_verification AND state<>'closed') OR
   EXISTS(SELECT 1 FROM gregale_copy_database_verification_retries.windows WHERE source_oid=p_source AND attempt>p_attempt) THEN
   RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='another verification attempt owns access';
  END IF;
  IF w.state<>'open' OR d.datallowconn OR d.datistemplate<>p_template OR d.datconnlimit<>p_limit THEN
   RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='database verification admission changed';
  END IF;
  IF NOT pg_catalog.has_database_privilege(current_user,p_target,'CONNECT') OR
   EXISTS(SELECT 1 FROM pg_catalog.pg_roles r WHERE r.rolcanlogin AND NOT r.rolsuper AND r.rolname<>current_user AND
    (pg_catalog.has_database_privilege(r.oid,p_target,'CONNECT') OR pg_catalog.pg_has_role(r.oid,p_owner,'MEMBER'))) THEN
   RAISE EXCEPTION USING ERRCODE='0A000',MESSAGE='database verification customer admission is not isolated';
  END IF;
  IF EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE datid=p_target) THEN
   RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='database verification has active sessions';
  END IF;
  EXECUTE pg_catalog.format('ALTER DATABASE %I ALLOW_CONNECTIONS true IS_TEMPLATE false CONNECTION LIMIT %s',p_name,p_verification_limit);
 ELSIF p_action='quiesce' THEN
  IF w.state NOT IN ('open','closing') THEN
   RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='database verification window is terminal';
  END IF;
  -- Close first, even after role/catalogue drift or cancellation. Restoration
  -- and a successful closure receipt require subsequent full verification.
  EXECUTE pg_catalog.format('ALTER DATABASE %I ALLOW_CONNECTIONS false IS_TEMPLATE false CONNECTION LIMIT %s',p_name,p_verification_limit);
  UPDATE gregale_copy_database_verification_retries.windows SET state='closing' WHERE source_oid=p_source AND owner_id=p_verification;
 ELSIF p_action='finish' THEN
  IF w.state<>'closing' OR d.datallowconn OR d.datistemplate OR d.datconnlimit<>p_verification_limit OR
   EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE datid=p_target) THEN
   RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='database verification closure is not quiescent';
  END IF;
  EXECUTE pg_catalog.format('ALTER DATABASE %I ALLOW_CONNECTIONS false IS_TEMPLATE %s CONNECTION LIMIT %s',p_name,CASE WHEN p_template THEN 'true' ELSE 'false' END,p_limit);
  UPDATE gregale_copy_database_verification_retries.windows SET state='closed',closed_at=clock_timestamp() WHERE source_oid=p_source AND owner_id=p_verification;
 ELSE
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid database verification action';
 END IF;
 RETURN true;
END;
$body$;

-- name: ChangeCopyDatabaseVerificationRetryAdmission :one
SELECT pg_temp.gregale_copy_database_verification_retries_change(
 sqlc.arg(source_oid)::oid,sqlc.arg(target_oid)::oid,sqlc.arg(database_name)::text,
 sqlc.arg(owner_oid)::oid,sqlc.arg(verification_owner)::uuid,sqlc.arg(plan_fingerprint)::text,
 sqlc.arg(prepared_at)::timestamptz,sqlc.arg(action)::text,sqlc.arg(original_template)::boolean,
 sqlc.arg(original_limit)::integer,sqlc.arg(verification_limit)::integer,
 sqlc.arg(import_owner)::uuid,sqlc.arg(import_opened)::timestamptz,sqlc.arg(import_closed)::timestamptz,
 sqlc.arg(attempt)::integer,sqlc.arg(previous_owner)::uuid,sqlc.arg(previous_opened)::timestamptz,sqlc.arg(previous_closed)::timestamptz)::boolean;
