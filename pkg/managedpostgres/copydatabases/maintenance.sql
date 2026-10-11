-- The creation namespace stays unchanged so existing receipts remain readable.
-- Maintenance owns only one immutable dispatch window per database. A retry
-- closes/observes that original window; it never executes another callback.
-- name: CopyDatabaseMaintenanceSchemaExists :one
SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname='gregale_copy_database_maintenance')::boolean;

-- name: InstallCopyDatabaseMaintenanceSchema :exec
CREATE SCHEMA gregale_copy_database_maintenance;

-- name: InstallCopyDatabaseMaintenanceWindows :exec
CREATE TABLE gregale_copy_database_maintenance.windows (
 source_oid oid PRIMARY KEY CHECK (source_oid<>0),
 target_oid oid NOT NULL UNIQUE CHECK (target_oid<>0),
 owner_id uuid NOT NULL UNIQUE CHECK (owner_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 plan_fingerprint text NOT NULL CHECK (plan_fingerprint ~ '^[0-9a-f]{64}$'),
 preparation_created_at timestamptz NOT NULL,
 state text NOT NULL CHECK (state IN ('open','closing','closed')),
 opened_at timestamptz NOT NULL, closed_at timestamptz,
 CHECK (isfinite(preparation_created_at) AND isfinite(opened_at) AND opened_at>=preparation_created_at AND
  ((state IN ('open','closing') AND closed_at IS NULL) OR
   (state='closed' AND closed_at IS NOT NULL AND isfinite(closed_at) AND closed_at>=opened_at)))
);

-- name: InstallCopyDatabaseMaintenanceActiveIndex :exec
CREATE UNIQUE INDEX windows_one_active ON gregale_copy_database_maintenance.windows ((1)) WHERE state IN ('open','closing');

-- name: PrivateCopyDatabaseMaintenanceJournal :one
SELECT EXISTS (
 SELECT 1 FROM pg_catalog.pg_namespace n WHERE n.nspname='gregale_copy_database_maintenance'
 AND n.nspowner=(SELECT oid FROM pg_catalog.pg_roles WHERE rolname=current_user)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.aclexplode(COALESCE(n.nspacl,pg_catalog.acldefault('n',n.nspowner))) a WHERE a.grantee<>n.nspowner)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p WHERE p.pronamespace=n.oid)
 AND (SELECT count(*) FROM pg_catalog.pg_class c WHERE c.relnamespace=n.oid AND c.relkind<>'i')=1
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_class c WHERE c.relnamespace=n.oid AND c.relkind='r' AND c.relname='windows')
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_index i ON i.indexrelid=c.oid
  WHERE c.relnamespace=n.oid AND c.relname='windows_one_active' AND i.indisunique AND i.indisvalid AND i.indisready
  AND i.indrelid=(SELECT oid FROM pg_catalog.pg_class WHERE relnamespace=n.oid AND relname='windows')
  AND i.indpred IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c WHERE c.relnamespace=n.oid AND
  (c.relowner<>n.nspowner OR c.relrowsecurity OR c.relforcerowsecurity OR
   EXISTS(SELECT 1 FROM pg_catalog.aclexplode(COALESCE(c.relacl,pg_catalog.acldefault('r',c.relowner))) a WHERE a.grantee<>c.relowner) OR
   EXISTS(SELECT 1 FROM pg_catalog.pg_trigger t WHERE t.tgrelid=c.oid) OR
   EXISTS(SELECT 1 FROM pg_catalog.pg_rewrite r WHERE r.ev_class=c.oid)))
 AND (SELECT pg_catalog.array_agg(a.attname::text ORDER BY a.attnum) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=n.oid AND c.relname='windows' AND a.attnum>0 AND NOT a.attisdropped)=ARRAY['source_oid','target_oid','owner_id','plan_fingerprint','preparation_created_at','state','opened_at','closed_at']::text[]
 AND (SELECT pg_catalog.array_agg(a.atttypid::regtype::text ORDER BY a.attnum) FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=n.oid AND c.relname='windows' AND a.attnum>0 AND NOT a.attisdropped)=ARRAY['oid','oid','uuid','text','timestamp with time zone','text','timestamp with time zone','timestamp with time zone']::text[]
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=n.oid AND c.relkind='r' AND a.attnum>0 AND
  (a.attisdropped OR a.attgenerated<>'' OR (NOT a.attnotnull AND a.attname<>'closed_at')))
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_attrdef a JOIN pg_catalog.pg_class c ON c.oid=a.adrelid WHERE c.relnamespace=n.oid)
)::boolean;

-- name: CopyDatabaseMaintenanceWindows :many
SELECT * FROM gregale_copy_database_maintenance.windows ORDER BY source_oid;

-- name: InsertCopyDatabaseMaintenanceWindow :exec
INSERT INTO gregale_copy_database_maintenance.windows
(source_oid,target_oid,owner_id,plan_fingerprint,preparation_created_at,state,opened_at)
VALUES ($1::oid,$2::oid,$3::uuid,$4::text,$5::timestamptz,'open',clock_timestamp());

-- Functions are invoker-only in the dedicated connection's pg_temp namespace.
-- No database ACL is rewritten: normal GRANT/REVOKE cannot restore a NULL ACL.
-- Exact login/ownership capability checks therefore precede opening admission.
-- Provider administrators (superusers) are outside customer SQL admission; the
-- enclosing borrower must independently authenticate provider isolation.
-- name: InstallCopyDatabaseMaintenanceMutation :exec
CREATE OR REPLACE FUNCTION pg_temp.gregale_copy_database_maintenance_change(
 p_source oid,p_target oid,p_name text,p_owner oid,p_dispatch uuid,
 p_fingerprint text,p_prepared timestamptz,p_action text,p_template boolean,
 p_limit integer,p_maintenance_limit integer) RETURNS boolean
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $body$
DECLARE
 d pg_catalog.pg_database%ROWTYPE;
 w gregale_copy_database_maintenance.windows%ROWTYPE;
BEGIN
 SELECT * INTO STRICT w FROM gregale_copy_database_maintenance.windows WHERE source_oid=p_source FOR UPDATE;
 IF w.target_oid<>p_target OR w.owner_id<>p_dispatch OR w.plan_fingerprint<>p_fingerprint OR w.preparation_created_at<>p_prepared THEN
  RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='database maintenance ownership changed';
 END IF;
 SELECT * INTO STRICT d FROM pg_catalog.pg_database WHERE oid=p_target;
 IF d.datname::text<>p_name OR d.datdba<>p_owner OR d.datname=current_database() THEN
  RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='database maintenance identity changed';
 END IF;
 IF NOT pg_catalog.pg_has_role(current_user,p_owner,'USAGE') THEN
  RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='database maintenance owner authority unavailable';
 END IF;
 IF p_action='open' THEN
  IF w.state<>'open' OR d.datallowconn OR d.datistemplate<>p_template OR d.datconnlimit<>p_limit THEN
   RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='database maintenance admission changed';
  END IF;
  IF NOT pg_catalog.has_database_privilege(current_user,p_target,'CONNECT') OR
   EXISTS(SELECT 1 FROM pg_catalog.pg_roles r WHERE r.rolcanlogin AND NOT r.rolsuper AND r.rolname<>current_user AND
    (pg_catalog.has_database_privilege(r.oid,p_target,'CONNECT') OR pg_catalog.pg_has_role(r.oid,p_owner,'MEMBER'))) THEN
   RAISE EXCEPTION USING ERRCODE='0A000',MESSAGE='database maintenance customer admission is not isolated';
  END IF;
  IF EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE datid=p_target) THEN
   RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='database maintenance has active sessions';
  END IF;
  EXECUTE pg_catalog.format('ALTER DATABASE %I ALLOW_CONNECTIONS true IS_TEMPLATE false CONNECTION LIMIT %s',p_name,p_maintenance_limit);
 ELSIF p_action='quiesce' THEN
  IF w.state NOT IN ('open','closing') THEN
   RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='database maintenance window is terminal';
  END IF;
  -- Close first, even after role/catalogue drift or cancellation. Restoration
  -- and a successful closure receipt require subsequent full verification.
  EXECUTE pg_catalog.format('ALTER DATABASE %I ALLOW_CONNECTIONS false IS_TEMPLATE false CONNECTION LIMIT %s',p_name,p_maintenance_limit);
  UPDATE gregale_copy_database_maintenance.windows SET state='closing' WHERE source_oid=p_source;
 ELSIF p_action='finish' THEN
  IF w.state<>'closing' OR d.datallowconn OR d.datistemplate OR d.datconnlimit<>p_maintenance_limit OR
   EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE datid=p_target) THEN
   RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='database maintenance closure is not quiescent';
  END IF;
  EXECUTE pg_catalog.format('ALTER DATABASE %I ALLOW_CONNECTIONS false IS_TEMPLATE %s CONNECTION LIMIT %s',p_name,CASE WHEN p_template THEN 'true' ELSE 'false' END,p_limit);
  UPDATE gregale_copy_database_maintenance.windows SET state='closed',closed_at=clock_timestamp() WHERE source_oid=p_source;
 ELSE
  RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid database maintenance action';
 END IF;
 RETURN true;
END;
$body$;

-- name: ChangeCopyDatabaseMaintenanceAdmission :one
SELECT pg_temp.gregale_copy_database_maintenance_change(
 sqlc.arg(source_oid)::oid,sqlc.arg(target_oid)::oid,sqlc.arg(database_name)::text,
 sqlc.arg(owner_oid)::oid,sqlc.arg(dispatch_owner)::uuid,sqlc.arg(plan_fingerprint)::text,
 sqlc.arg(prepared_at)::timestamptz,sqlc.arg(action)::text,sqlc.arg(original_template)::boolean,
 sqlc.arg(original_limit)::integer,sqlc.arg(maintenance_limit)::integer)::boolean;

-- name: CopyDatabaseMaintenanceSessionsPrivate :one
SELECT NOT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity
 WHERE datid=$1::oid AND usename::text<>current_user)::boolean
 AND (SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE datid=$1::oid)<=$2::integer;

-- Closing needs to authenticate the borrower even if seed catalogue drift would
-- reject an import. This query never grants SQL dispatch or role authority.
-- name: CopyDatabaseMaintenanceBootstrapIdentity :one
SELECT pg_catalog.current_setting('server_version_num')::integer AS server_version,
 pg_catalog.current_database()::text AS database_name,d.oid AS database_oid,
 current_user::text AS role_name,r.oid AS role_oid,session_user::text AS session_role,
 pg_catalog.current_setting('transaction_read_only')::boolean AS read_only
FROM pg_catalog.pg_database d,pg_catalog.pg_roles r
WHERE d.datname=pg_catalog.current_database() AND r.rolname=current_user;
