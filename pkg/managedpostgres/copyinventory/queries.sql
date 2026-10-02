-- name: CopyClusterIdentity :one
SELECT current_setting('server_version_num')::integer AS server_version,
 current_database()::text AS database_name, d.oid AS database_oid,
 current_user::text AS role_name, r.oid AS role_oid, session_user::text AS session_role,
 current_setting('transaction_read_only')::boolean AS read_only,
 current_setting('transaction_isolation')::text AS isolation
FROM pg_database d, pg_roles r WHERE d.datname=current_database() AND r.rolname=current_user;

-- Include templates, provider/system databases and private maintenance resources.
-- The later copy plan must explicitly classify every entry instead of hiding it.
-- JSON access to locale fields remains portable across supported server majors.
-- name: CopyClusterDatabases :one
SELECT coalesce(jsonb_agg(jsonb_build_object(
 'oid',d.oid::bigint,'name',d.datname,'owner_oid',d.datdba::bigint,'owner',r.rolname,
 'encoding',d.encoding,'template',d.datistemplate,'allow_connections',d.datallowconn,
 'connection_limit',d.datconnlimit,'tablespace_oid',d.dattablespace::bigint,
 'collation',d.datcollate,'ctype',d.datctype,'acl',d.datacl,
 'locale_provider',to_jsonb(d)->>'datlocprovider',
 'locale',coalesce(to_jsonb(d)->>'datlocale',to_jsonb(d)->>'daticulocale'),
 'collation_version',to_jsonb(d)->>'datcollversion') ORDER BY d.datname),'[]'::jsonb)::jsonb AS records
FROM pg_database d LEFT JOIN pg_roles r ON r.oid=d.datdba;

-- Passwords and password hashes are excluded. Stage credentials must be new.
-- name: CopyClusterRoles :one
SELECT coalesce(jsonb_agg(jsonb_build_object(
 'oid',r.oid::bigint,'name',r.rolname,'superuser',r.rolsuper,'inherit',r.rolinherit,
 'create_role',r.rolcreaterole,'create_database',r.rolcreatedb,'login',r.rolcanlogin,
 'replication',r.rolreplication,'connection_limit',r.rolconnlimit,
 'valid_until',r.rolvaliduntil,'bypass_rls',r.rolbypassrls,'config',r.rolconfig)
 ORDER BY r.rolname),'[]'::jsonb)::jsonb AS records FROM pg_roles r;

-- Before PG16, INHERIT follows the member role and SET is always permitted.
-- Preserve grantor and ADMIN as well as the effective INHERIT/SET semantics.
-- name: CopyClusterMemberships :one
SELECT coalesce(jsonb_agg(jsonb_build_object(
 'role_oid',a.roleid::bigint,'role',r.rolname,'member_oid',a.member::bigint,'member',m.rolname,
 'grantor_oid',a.grantor::bigint,'grantor',g.rolname,'admin',a.admin_option,
 'inherit',coalesce((to_jsonb(a)->>'inherit_option')::boolean,m.rolinherit),
 'set',coalesce((to_jsonb(a)->>'set_option')::boolean,true))
 ORDER BY r.rolname,m.rolname,g.rolname),'[]'::jsonb)::jsonb AS records
FROM pg_auth_members a LEFT JOIN pg_roles r ON r.oid=a.roleid
 LEFT JOIN pg_roles m ON m.oid=a.member LEFT JOIN pg_roles g ON g.oid=a.grantor;

-- Read cluster-role, database and role-in-database settings, including databases
-- which currently refuse connections. These values may contain customer secrets.
-- name: CopyClusterSettings :one
SELECT coalesce(jsonb_agg(jsonb_build_object(
 'database_oid',s.setdatabase::bigint,'database',coalesce(d.datname::text,''),
 'role_oid',s.setrole::bigint,'role',coalesce(r.rolname::text,''),'config',s.setconfig)
 ORDER BY s.setdatabase,s.setrole),'[]'::jsonb)::jsonb AS records
FROM pg_db_role_setting s LEFT JOIN pg_database d ON d.oid=s.setdatabase
 LEFT JOIN pg_roles r ON r.oid=s.setrole;

-- Physical paths require target-specific mapping and are not copied verbatim.
-- name: CopyClusterTablespaces :one
SELECT coalesce(jsonb_agg(jsonb_build_object(
 'oid',s.oid::bigint,'name',s.spcname,'owner_oid',s.spcowner::bigint,'owner',r.rolname,
 'acl',s.spcacl,'options',s.spcoptions) ORDER BY s.spcname),'[]'::jsonb)::jsonb AS records
FROM pg_tablespace s LEFT JOIN pg_roles r ON r.oid=s.spcowner;

-- Prepared transactions cannot be reconstructed by pg_dump. They remain explicit
-- input to capture qualification; their presence must never be silently skipped.
-- name: CopyClusterPreparedTransactions :one
SELECT coalesce(jsonb_agg(jsonb_build_object(
 'transaction',p.transaction::text,'gid',p.gid,'prepared',p.prepared,
 'owner',p.owner,'database',p.database) ORDER BY p.database,p.gid),'[]'::jsonb)::jsonb AS records
FROM pg_prepared_xacts p;
-- A borrowed customer connection may have an application-controlled search path.
-- This is transaction-local and cannot change the source's persistent settings.
-- name: SetCopyInventorySearchPath :exec
SET LOCAL search_path = pg_catalog;
