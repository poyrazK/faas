-- name: ProbeManagedPostgresCredential :one
SELECT session_user::text AS login, current_user::text AS effective_user,
 current_database()::text AS database_name, current_setting('server_version_num')::integer AS version_num,
 current_setting('transaction_read_only')::boolean AS read_only,
 current_setting('row_security')='on' AS row_security,
 has_schema_privilege(current_user,'public','USAGE') AS schema_usage,
 has_schema_privilege(current_user,'public','CREATE') AS schema_create,
 (r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls OR r.rolinherit
 OR e.rolsuper OR e.rolcreatedb OR e.rolcreaterole OR e.rolreplication OR e.rolbypassrls OR e.rolinherit) AS unsafe_role,
 ((sqlc.arg(access)::text<>'migration' AND has_database_privilege(current_user,current_database(),'CREATE'))
 OR has_database_privilege(current_user,current_database(),'TEMPORARY')
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid
 AND (sqlc.arg(access)::text<>'migration' OR roleid<>e.oid))
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=e.oid AND e.oid<>r.oid)) AS elevated_runtime,
 NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND ((c.relkind IN ('r','p','v','m','f') AND NOT
 (has_table_privilege(current_user,c.oid,'SELECT') AND
 ((sqlc.arg(access)::text='read_only' AND NOT (has_table_privilege(current_user,c.oid,'INSERT')
 OR has_table_privilege(current_user,c.oid,'UPDATE') OR has_table_privilege(current_user,c.oid,'DELETE')))
 OR (sqlc.arg(access)::text<>'read_only' AND has_table_privilege(current_user,c.oid,'INSERT')
 AND has_table_privilege(current_user,c.oid,'UPDATE') AND has_table_privilege(current_user,c.oid,'DELETE')))
 AND (sqlc.arg(access)::text='migration' OR
 (c.relowner<>e.oid AND NOT has_table_privilege(current_user,c.oid,'TRUNCATE')))))
 OR (c.relkind='S' AND NOT (has_sequence_privilege(current_user,c.oid,'SELECT') AND
 ((sqlc.arg(access)::text='read_only' AND NOT (has_sequence_privilege(current_user,c.oid,'USAGE')
 OR has_sequence_privilege(current_user,c.oid,'UPDATE'))) OR (sqlc.arg(access)::text<>'read_only'
 AND has_sequence_privilege(current_user,c.oid,'USAGE')))))))
 AND (sqlc.arg(access)::text<>'read_only' OR NOT (
 EXISTS (SELECT 1 FROM pg_catalog.pg_namespace n WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema'
 AND has_schema_privilege(current_user,n.oid,'CREATE'))
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND
 ((c.relkind IN ('r','p','v','m','f') AND (has_table_privilege(current_user,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
 OR has_any_column_privilege(current_user,c.oid,'INSERT,UPDATE,REFERENCES')))
 OR (c.relkind='S' AND has_sequence_privilege(current_user,c.oid,'USAGE,UPDATE'))))
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND p.prosecdef AND has_function_privilege(current_user,p.oid,'EXECUTE'))
 OR EXISTS (SELECT 1 FROM pg_catalog.pg_default_acl d, LATERAL aclexplode(d.defaclacl) a
 WHERE a.grantee IN (0,e.oid) AND
 ((d.defaclobjtype='r' AND a.privilege_type IN ('INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER'))
 OR (d.defaclobjtype='S' AND a.privilege_type IN ('USAGE','UPDATE')))))) AS data_access
FROM pg_catalog.pg_roles r JOIN pg_catalog.pg_roles e ON e.rolname=current_user WHERE r.rolname=session_user;
