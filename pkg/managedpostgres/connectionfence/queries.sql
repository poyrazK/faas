-- name: MaintenanceIdentity :one
SELECT current_database()::text AS database_name, current_user::text AS role_name,
 d.oid AS database_oid, d.datdba AS owner_oid,
 pg_has_role(current_user,d.datdba,'USAGE') AS owns_database,
 d.datallowconn AS allows_connections,
 NOT EXISTS (SELECT 1 FROM aclexplode(COALESCE(d.datacl, acldefault('d',d.datdba))) a
   WHERE a.privilege_type='CONNECT' AND a.grantee<>d.datdba) AS private_connections,
 NOT EXISTS (SELECT 1 FROM pg_roles r WHERE r.rolcanlogin AND r.rolname<>current_user
   AND NOT r.rolsuper AND (pg_has_role(r.oid,d.datdba,'MEMBER') OR
     (current_setting('server_version_num')::integer<160000 AND r.rolcreaterole))) AS private_role,
 NOT EXISTS (SELECT 1 FROM pg_stat_activity a WHERE a.datid=d.oid AND a.usename IS DISTINCT FROM current_user) AS private_sessions
FROM pg_database d WHERE d.datname=current_database();

-- name: FenceSchemaExists :one
SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname='gregale_checkpoint') AS present;

-- Inventory the entire source, including templates, databases which refuse
-- connections, and databases owned by another role. Filtering those out would
-- silently omit writers. Only the authenticated private maintenance DB is exempt.
-- name: CheckpointDatabaseNames :many
SELECT d.datname::text AS database_name FROM pg_catalog.pg_database d
WHERE d.datname<>sqlc.arg(maintenance_database)::text
ORDER BY d.datname COLLATE "C" LIMIT sqlc.arg(max_databases)::integer;

-- name: FenceSchemaPrivate :one
SELECT n.nspowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
 AND NOT EXISTS (SELECT 1 FROM aclexplode(COALESCE(n.nspacl,acldefault('n',n.nspowner))) a WHERE a.grantee<>n.nspowner)
 AND NOT EXISTS (SELECT 1 FROM pg_class c WHERE c.relnamespace=n.oid
   AND (c.relowner<>n.nspowner OR EXISTS (SELECT 1 FROM aclexplode(c.relacl) a WHERE a.grantee<>n.nspowner)))
 AND NOT EXISTS (SELECT 1 FROM pg_proc p WHERE p.pronamespace=n.oid
   AND (p.proowner<>n.nspowner OR p.prosecdef OR EXISTS (SELECT 1 FROM aclexplode(COALESCE(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee<>n.nspowner)))
 AS private_schema
FROM pg_namespace n WHERE n.nspname='gregale_checkpoint';

-- name: FenceInstallationVersion :one
SELECT version FROM gregale_checkpoint.installation WHERE singleton=true;

-- name: InstallFenceSchema :exec
CREATE SCHEMA gregale_checkpoint;

-- name: InstallFenceVersion :exec
CREATE TABLE gregale_checkpoint.installation (
 singleton boolean PRIMARY KEY CHECK (singleton), version integer NOT NULL CHECK (version=1)
);

-- name: RecordFenceVersion :exec
INSERT INTO gregale_checkpoint.installation (singleton,version) VALUES (true,1);

-- name: InstallFenceTable :exec
CREATE TABLE gregale_checkpoint.connection_fences (
 owner_token uuid PRIMARY KEY,
 source_resource_id text NOT NULL CHECK (source_resource_id<>''),
 state text NOT NULL CHECK (state IN ('closed','released','abandoned')),
 database_names text[] NOT NULL CHECK (cardinality(database_names)>0 OR state='abandoned'),
 closed_at timestamptz, released_at timestamptz,
 CHECK ((state='closed' AND closed_at IS NOT NULL AND released_at IS NULL) OR
   (state='released' AND closed_at IS NOT NULL AND released_at IS NOT NULL) OR
   (state='abandoned' AND closed_at IS NULL AND released_at IS NOT NULL AND cardinality(database_names)=0))
);

-- name: InstallFenceActiveIndex :exec
CREATE UNIQUE INDEX connection_fences_one_active ON gregale_checkpoint.connection_fences ((1)) WHERE state='closed';

-- name: InstallFenceDatabaseTable :exec
CREATE TABLE gregale_checkpoint.connection_fence_databases (
 owner_token uuid NOT NULL REFERENCES gregale_checkpoint.connection_fences(owner_token) ON DELETE RESTRICT,
 database_oid oid NOT NULL, database_name text NOT NULL CHECK (database_name<>''), owner_oid oid NOT NULL,
 original_allow_connections boolean NOT NULL,
 PRIMARY KEY (owner_token,database_oid), UNIQUE (owner_token,database_name)
);

-- name: InstallFenceCloseFunction :exec
CREATE FUNCTION gregale_checkpoint.close_connections(token uuid, source text, names text[]) RETURNS boolean
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog,gregale_checkpoint AS $$
DECLARE previous gregale_checkpoint.connection_fences%ROWTYPE; db record; normalized text[];
BEGIN
 LOCK TABLE gregale_checkpoint.connection_fences IN SHARE ROW EXCLUSIVE MODE;
 SELECT array_agg(name ORDER BY name COLLATE "C") INTO normalized FROM unnest(names) name;
 IF token IS NULL OR token='00000000-0000-0000-0000-000000000000' OR source IS NULL OR source='' OR
   normalized IS NULL OR cardinality(normalized)<>cardinality(ARRAY(SELECT DISTINCT unnest(names))) OR
   EXISTS (SELECT 1 FROM unnest(names) name WHERE name IS NULL OR name=current_database() OR name='') THEN
  RAISE EXCEPTION 'invalid connection fence' USING ERRCODE='22023';
 END IF;
 SELECT * INTO previous FROM gregale_checkpoint.connection_fences WHERE owner_token=token;
 IF FOUND THEN
  IF previous.source_resource_id<>source OR previous.database_names<>normalized OR previous.state<>'closed' THEN
   RAISE EXCEPTION 'connection fence owner conflict' USING ERRCODE='55000';
  END IF;
  IF EXISTS (SELECT 1 FROM gregale_checkpoint.connection_fence_databases f LEFT JOIN pg_database d ON d.oid=f.database_oid
    WHERE f.owner_token=token AND (d.oid IS NULL OR d.datname<>f.database_name OR d.datdba<>f.owner_oid OR d.datallowconn)) THEN
   RAISE EXCEPTION 'connection fence identity changed' USING ERRCODE='55000';
  END IF;
  RETURN true;
 END IF;
 IF EXISTS (SELECT 1 FROM gregale_checkpoint.connection_fences WHERE state='closed') THEN
  RAISE EXCEPTION 'another connection fence is active' USING ERRCODE='55000';
 END IF;
 IF (SELECT count(*) FROM pg_database WHERE datname=ANY(names))<>cardinality(names) OR
   EXISTS (SELECT 1 FROM pg_database WHERE datname=ANY(names) AND datdba<>(SELECT oid FROM pg_roles WHERE rolname=current_user)) THEN
  RAISE EXCEPTION 'source databases are not owned by maintenance role' USING ERRCODE='55000';
 END IF;
 INSERT INTO gregale_checkpoint.connection_fences VALUES (token,source,'closed',normalized,clock_timestamp(),NULL);
 FOR db IN SELECT oid,datname,datdba,datallowconn FROM pg_database WHERE datname=ANY(names) ORDER BY oid LOOP
  INSERT INTO gregale_checkpoint.connection_fence_databases VALUES (token,db.oid,db.datname,db.datdba,db.datallowconn);
  EXECUTE format('ALTER DATABASE %I ALLOW_CONNECTIONS false',db.datname);
 END LOOP;
 IF EXISTS (SELECT 1 FROM gregale_checkpoint.connection_fence_databases f LEFT JOIN pg_database d ON d.oid=f.database_oid
   WHERE f.owner_token=token AND (d.oid IS NULL OR d.datname<>f.database_name OR d.datdba<>f.owner_oid OR d.datallowconn)) THEN
  RAISE EXCEPTION 'connection fence identity changed' USING ERRCODE='55000';
 END IF;
 RETURN true;
END $$;

-- name: RevokeFenceCloseFunction :exec
REVOKE ALL ON FUNCTION gregale_checkpoint.close_connections(uuid,text,text[]) FROM PUBLIC;

-- name: InstallFenceReleaseFunction :exec
CREATE FUNCTION gregale_checkpoint.release_connections(token uuid, source text) RETURNS boolean
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog,gregale_checkpoint AS $$
DECLARE previous gregale_checkpoint.connection_fences%ROWTYPE; db record;
BEGIN
 LOCK TABLE gregale_checkpoint.connection_fences IN SHARE ROW EXCLUSIVE MODE;
 SELECT * INTO previous FROM gregale_checkpoint.connection_fences WHERE owner_token=token;
 IF NOT FOUND OR previous.source_resource_id<>source THEN
  RAISE EXCEPTION 'connection fence owner conflict' USING ERRCODE='55000';
 END IF;
 IF previous.state='released' THEN RETURN true; END IF;
 IF previous.state<>'closed' THEN
  RAISE EXCEPTION 'connection fence was abandoned' USING ERRCODE='55000';
 END IF;
 IF EXISTS (SELECT 1 FROM gregale_checkpoint.connection_fence_databases f LEFT JOIN pg_database d ON d.oid=f.database_oid
   WHERE f.owner_token=token AND (d.oid IS NULL OR d.datname<>f.database_name OR d.datdba<>f.owner_oid OR d.datallowconn)) THEN
  RAISE EXCEPTION 'connection fence identity changed' USING ERRCODE='55000';
 END IF;
 FOR db IN SELECT * FROM gregale_checkpoint.connection_fence_databases WHERE owner_token=token ORDER BY database_oid LOOP
  EXECUTE format('ALTER DATABASE %I ALLOW_CONNECTIONS %s',db.database_name,CASE WHEN db.original_allow_connections THEN 'true' ELSE 'false' END);
 END LOOP;
 IF EXISTS (SELECT 1 FROM gregale_checkpoint.connection_fence_databases f LEFT JOIN pg_database d ON d.oid=f.database_oid
   WHERE f.owner_token=token AND (d.oid IS NULL OR d.datname<>f.database_name OR d.datdba<>f.owner_oid OR d.datallowconn<>f.original_allow_connections)) THEN
  RAISE EXCEPTION 'connection fence identity changed' USING ERRCODE='55000';
 END IF;
 UPDATE gregale_checkpoint.connection_fences SET state='released',released_at=clock_timestamp() WHERE owner_token=token;
 RETURN true;
END $$;

-- name: RevokeFenceReleaseFunction :exec
REVOKE ALL ON FUNCTION gregale_checkpoint.release_connections(uuid,text) FROM PUBLIC;

-- name: InstallFenceAbandonFunction :exec
CREATE FUNCTION gregale_checkpoint.abandon_connections(token uuid, source text) RETURNS boolean
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog,gregale_checkpoint AS $$
DECLARE previous gregale_checkpoint.connection_fences%ROWTYPE;
BEGIN
 LOCK TABLE gregale_checkpoint.connection_fences IN SHARE ROW EXCLUSIVE MODE;
 IF token IS NULL OR token='00000000-0000-0000-0000-000000000000' OR source IS NULL OR source='' THEN
  RAISE EXCEPTION 'invalid connection fence' USING ERRCODE='22023';
 END IF;
 SELECT * INTO previous FROM gregale_checkpoint.connection_fences WHERE owner_token=token;
 IF NOT FOUND THEN
  -- This terminal marker also fences a close request that has not reached
  -- the server yet. It must not be removed while such requests can arrive.
  INSERT INTO gregale_checkpoint.connection_fences VALUES (token,source,'abandoned','{}',NULL,clock_timestamp());
  RETURN true;
 END IF;
 IF previous.source_resource_id<>source THEN
  RAISE EXCEPTION 'connection fence owner conflict' USING ERRCODE='55000';
 END IF;
 IF previous.state='closed' THEN
  PERFORM gregale_checkpoint.release_connections(token,source);
 END IF;
 RETURN true;
END $$;

-- name: RevokeFenceAbandonFunction :exec
REVOKE ALL ON FUNCTION gregale_checkpoint.abandon_connections(uuid,text) FROM PUBLIC;

-- name: CloseConnections :one
SELECT gregale_checkpoint.close_connections(sqlc.arg(owner_token)::uuid,sqlc.arg(source_resource_id)::text,sqlc.arg(database_names)::text[])::boolean AS closed;

-- name: ReleaseConnections :one
SELECT gregale_checkpoint.release_connections(sqlc.arg(owner_token)::uuid,sqlc.arg(source_resource_id)::text)::boolean AS released;

-- name: AbandonConnections :one
SELECT gregale_checkpoint.abandon_connections(sqlc.arg(owner_token)::uuid,sqlc.arg(source_resource_id)::text)::boolean AS abandoned;

-- name: ReadFence :one
SELECT * FROM gregale_checkpoint.connection_fences WHERE owner_token=sqlc.arg(owner_token)::uuid AND source_resource_id=sqlc.arg(source_resource_id)::text;

-- name: ReadFenceDatabases :many
SELECT f.*,d.oid IS NOT NULL AND d.datname=f.database_name AND d.datdba=f.owner_oid AND NOT d.datallowconn AS identity_closed,
 (SELECT count(*) FROM pg_catalog.pg_stat_activity a WHERE a.datid=f.database_oid) AS sessions,
 (SELECT count(*) FROM pg_catalog.pg_prepared_xacts p WHERE p.database=f.database_name) AS prepared_transactions
FROM gregale_checkpoint.connection_fence_databases f LEFT JOIN pg_database d ON d.oid=f.database_oid
WHERE f.owner_token=sqlc.arg(owner_token)::uuid ORDER BY f.database_name COLLATE "C";

-- Recheck the entire native catalogue against the original OIDs. A database
-- created after selection cannot disappear behind a drained selected subset.
-- name: CheckpointUnselectedDatabaseCount :one
SELECT count(*) FROM pg_catalog.pg_database d
WHERE d.datname <> sqlc.arg(maintenance_database)::text AND NOT EXISTS (
 SELECT 1 FROM gregale_checkpoint.connection_fence_databases f
 WHERE f.owner_token=sqlc.arg(owner_token)::uuid AND f.database_oid=d.oid
);
