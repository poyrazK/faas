-- ADR-20261023104734: daemons log in as members of faas_runtime, a NOLOGIN
-- group that holds row DML on the faas schema and owns nothing, so a daemon
-- cannot alter, drop, truncate or re-grant an object. The schema owner `faas`
-- stays the migration role. Run as a superuser in database faas; every
-- statement is idempotent. The first and last lines print a fingerprint of the
-- converged state so the caller can report whether anything changed.
\set ON_ERROR_STOP on
\set QUIET on
\pset tuples_only on
\pset format unaligned

\set fingerprint 'SELECT md5(concat_ws(''|'', (SELECT string_agg(r.rolname || '':'' || r.rolcanlogin || r.rolinherit, '','' ORDER BY r.rolname) FROM pg_catalog.pg_roles r WHERE r.rolname LIKE ''faas\\_%''), (SELECT string_agg(m.member::regrole::text, '','' ORDER BY m.member::regrole::text) FROM pg_catalog.pg_auth_members m WHERE m.roleid = to_regrole(''faas_runtime'')), (SELECT string_agg(c.relname || ''='' || coalesce(c.relacl::text, ''''), '','' ORDER BY c.relname) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = ''public''), (SELECT string_agg(d.defaclobjtype::text || ''='' || d.defaclacl::text, '','' ORDER BY d.defaclobjtype::text) FROM pg_catalog.pg_default_acl d), (SELECT n.nspacl::text FROM pg_catalog.pg_namespace n WHERE n.nspname = ''public''), (SELECT db.datacl::text FROM pg_catalog.pg_database db WHERE db.datname = current_database())))'
:fingerprint;

-- Peer-authenticated per-daemon roles, one per daemon OS user (pg_ident
-- faas_map). They have no password, so they cannot log in over TCP. The
-- password-authenticated faas_compute role is created by Ansible when the
-- operator provides its secret; it joins the group here.
DO $$
DECLARE
	r text;
BEGIN
	IF to_regrole('faas_runtime') IS NULL THEN
		CREATE ROLE faas_runtime NOLOGIN;
	END IF;
	ALTER ROLE faas_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
	FOREACH r IN ARRAY ARRAY['faas_apid', 'faas_schedd', 'faas_meterd', 'faas_githubd',
		'faas_outboundd', 'faas_bridged', 'faas_mcp_bridge', 'faas_shared'] LOOP
		IF to_regrole(r) IS NULL THEN
			EXECUTE format('CREATE ROLE %I LOGIN', r);
		END IF;
		EXECUTE format('ALTER ROLE %I LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS', r);
		IF NOT pg_has_role(r, 'faas_runtime', 'MEMBER') THEN
			EXECUTE format('GRANT faas_runtime TO %I', r);
		END IF;
	END LOOP;
	IF to_regrole('faas_compute') IS NOT NULL THEN
		ALTER ROLE faas_compute LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
		IF NOT pg_has_role('faas_compute', 'faas_runtime', 'MEMBER') THEN
			GRANT faas_runtime TO faas_compute;
		END IF;
	END IF;
END
$$;

GRANT CONNECT ON DATABASE faas TO faas_runtime;
GRANT USAGE ON SCHEMA public TO faas_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO faas_runtime;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO faas_runtime;
-- The migration ledger is the owner's: a daemon that could write it could mark
-- a migration applied without running it. Daemons only read it (db.MigrateUp).
-- The audit log is append-only for daemons: no code path updates or deletes
-- its rows (it outlives accounts, SOC 2 P5.1), so a compromised daemon cannot
-- rewrite or erase the record of what it did. Both are absent until the
-- migrations that create them run.
DO $$
BEGIN
	IF to_regclass('public.goose_db_version') IS NOT NULL THEN
		REVOKE INSERT, UPDATE, DELETE ON public.goose_db_version FROM faas_runtime;
	END IF;
	IF to_regclass('public.audit_log') IS NOT NULL THEN
		REVOKE UPDATE, DELETE ON public.audit_log FROM faas_runtime;
	END IF;
	-- ADR-20261026163015: anchors seal the audit log; like it, they are
	-- appended, never changed.
	IF to_regclass('public.audit_log_anchors') IS NOT NULL THEN
		REVOKE UPDATE, DELETE ON public.audit_log_anchors FROM faas_runtime;
	END IF;
	-- Issue #574: deployctl writes the release history as the owner; daemons
	-- only read it, so none can rewrite what was deployed.
	IF to_regclass('public.platform_release_activations') IS NOT NULL THEN
		REVOKE INSERT, UPDATE, DELETE ON public.platform_release_activations FROM faas_runtime;
	END IF;
END
$$;
-- Objects the next migrations create carry the same grants. Only objects the
-- owner creates are covered, which is every object: migrations run as faas.
ALTER DEFAULT PRIVILEGES FOR ROLE faas IN SCHEMA public
	GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO faas_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE faas IN SCHEMA public
	GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO faas_runtime;

:fingerprint;
