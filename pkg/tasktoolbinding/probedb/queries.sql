-- name: TaskBindingIdentity :one
SELECT session_user::text AS login, current_user::text AS role_name,
 pg_catalog.current_database()::text AS database_name,
 pg_catalog.current_schema()::text AS schema_name,
 COALESCE(pg_catalog.inet_server_addr()::text,'')::text AS server_address,
 COALESCE(pg_catalog.inet_server_port(),0)::integer AS server_port,
 n.nspowner::pg_catalog.regrole::text AS schema_owner,
 (r.rolsuper OR r.rolcreaterole OR r.rolcreatedb OR r.rolreplication OR r.rolbypassrls
 OR e.rolsuper OR e.rolcreaterole OR e.rolcreatedb OR e.rolreplication OR e.rolbypassrls)::boolean AS privileged,
 pg_catalog.has_schema_privilege(current_user,n.oid,'CREATE')::boolean AS can_create,
 pg_catalog.pg_has_role(current_user,n.nspowner,'MEMBER')::boolean AS owner_member,
 pg_catalog.has_database_privilege(current_user,pg_catalog.current_database(),'CREATE')::boolean AS database_create,
 pg_catalog.has_database_privilege(current_user,pg_catalog.current_database(),'TEMPORARY')::boolean AS temporary,
 (EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid AND roleid<>e.oid)
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=e.oid AND roleid<>e.oid))::boolean AS unsafe_membership,
 (pg_catalog.current_setting('transaction_read_only')='on')::boolean AS read_only,
 (pg_catalog.current_setting('row_security')='on')::boolean AS row_security
FROM pg_catalog.pg_roles r JOIN pg_catalog.pg_roles e ON e.rolname=current_user
JOIN pg_catalog.pg_namespace n ON n.nspname=pg_catalog.current_schema()
WHERE r.rolname=session_user;

-- name: TaskBindingTablePrivileges :many
SELECT c.relname::text AS name,
 pg_catalog.has_table_privilege(current_user,c.oid,'SELECT')::boolean AS can_read,
 (pg_catalog.has_table_privilege(current_user,c.oid,'INSERT')
 AND pg_catalog.has_table_privilege(current_user,c.oid,'UPDATE')
 AND pg_catalog.has_table_privilege(current_user,c.oid,'DELETE'))::boolean AS can_dml,
 (pg_catalog.has_table_privilege(current_user,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER')
 OR pg_catalog.has_any_column_privilege(current_user,c.oid,'INSERT,UPDATE'))::boolean AS can_write,
 (pg_catalog.has_table_privilege(current_user,c.oid,'TRUNCATE,TRIGGER,REFERENCES')
 OR pg_catalog.has_any_column_privilege(current_user,c.oid,'REFERENCES'))::boolean AS can_administer,
 (c.relowner=n.nspowner)::boolean AS owned_by_schema_owner
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname=pg_catalog.current_schema() AND c.relkind IN ('r','p') AND c.relname ~ '^gregale_mcp_task'
ORDER BY c.relname LIMIT sqlc.arg(max_tables)::integer;

-- name: TaskBindingObserverUnsafe :one
SELECT (EXISTS(SELECT 1 FROM pg_catalog.pg_namespace n
 WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema'
 AND pg_catalog.has_schema_privilege(current_user,n.oid,'CREATE'))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND
 ((c.relkind IN ('r','p','v','m','f') AND
 (pg_catalog.has_table_privilege(current_user,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
 OR pg_catalog.has_any_column_privilege(current_user,c.oid,'INSERT,UPDATE,REFERENCES')))
 OR (c.relkind='S' AND pg_catalog.has_sequence_privilege(current_user,c.oid,'USAGE,UPDATE'))))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND p.prosecdef
 AND pg_catalog.has_function_privilege(current_user,p.oid,'EXECUTE'))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_default_acl d, LATERAL pg_catalog.aclexplode(d.defaclacl) a
 WHERE (CASE WHEN a.grantee=0 THEN true ELSE pg_catalog.pg_has_role(current_user,a.grantee,'MEMBER') END)
 AND ((d.defaclobjtype='r' AND a.privilege_type IN ('INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER'))
 OR (d.defaclobjtype='S' AND a.privilege_type IN ('USAGE','UPDATE')))))::boolean AS unsafe;
