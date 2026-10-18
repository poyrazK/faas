-- name: ReadConnectionCapacity :one
SELECT pg_catalog.current_database()::text AS database_name,
 current_user::text AS role_name, session_user::text AS session_role,
 pg_catalog.current_setting('server_version_num')::integer AS server_version,
 pg_catalog.current_setting('max_connections')::bigint AS max_connections,
 pg_catalog.current_setting('superuser_reserved_connections')::bigint AS superuser_reserved,
 coalesce(nullif(pg_catalog.current_setting('reserved_connections',true),''),'0')::bigint AS reserved,
 d.datconnlimit::bigint AS database_limit, r.rolconnlimit::bigint AS role_limit
FROM pg_catalog.pg_database d CROSS JOIN pg_catalog.pg_roles r
WHERE d.datname=pg_catalog.current_database() AND r.rolname=session_user;
