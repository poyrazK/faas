-- ADR-20261110210000 slice 1c-2: enroll one compute node's database logins.
-- node_join.yml runs this as the postgres superuser on the control plane
-- before the node's daemons switch to them:
--
--   psql -X -v ON_ERROR_STOP=1 -d faas -v node=<compute_nodes.name> \
--        -v logins=<login>,<login>,... -f compute-node-db-logins.sql
--
-- Each login is a LOGIN role without a password, a member of faas_runtime
-- (row DML) and faas_node_logins (pg_hba admits it only by a fleet-CA client
-- certificate whose CN is the login), mapped to the node in
-- compute_node_db_logins, whose triggers confine its writes to that node.
-- A run is idempotent; a login already mapped to another node is refused.
SELECT set_config('faas_enroll.node', :'node', false) IS NOT NULL AS node_set;
SELECT set_config('faas_enroll.logins', :'logins', false) IS NOT NULL AS logins_set;

DO $$
DECLARE
	node text := current_setting('faas_enroll.node');
	logins text[] := string_to_array(current_setting('faas_enroll.logins'), ',');
	l text;
	mapped text;
BEGIN
	IF node !~ '^[A-Za-z0-9._-]{1,253}$' THEN
		RAISE EXCEPTION 'compute node name % is not a compute_nodes.name', node;
	END IF;
	IF coalesce(array_length(logins, 1), 0) = 0 THEN
		RAISE EXCEPTION 'no logins to enroll for node %', node;
	END IF;
	IF to_regrole('faas_runtime') IS NULL OR to_regrole('faas_node_logins') IS NULL THEN
		RAISE EXCEPTION 'converge runtime-roles.sql (faas_runtime, faas_node_logins) before enrolling node logins';
	END IF;
	FOREACH l IN ARRAY logins LOOP
		IF l !~ '^faas_[a-z0-9_]{1,58}$' THEN
			RAISE EXCEPTION 'login % does not fit compute_node_db_logins', l;
		END IF;
		SELECT d.node_name INTO mapped FROM public.compute_node_db_logins d WHERE d.login = l;
		IF mapped IS NOT NULL AND mapped <> node THEN
			RAISE EXCEPTION 'login % already belongs to node %, not %', l, mapped, node;
		END IF;
		IF mapped IS NULL AND to_regrole(l) IS NOT NULL THEN
			-- An existing role that no node owns is someone else's: a node
			-- login is only ever created here.
			RAISE EXCEPTION 'role % exists but is not a compute node login', l;
		END IF;
		IF to_regrole(l) IS NULL THEN
			EXECUTE format('CREATE ROLE %I LOGIN', l);
		END IF;
		EXECUTE format('ALTER ROLE %I LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD NULL', l);
		IF NOT pg_has_role(l, 'faas_runtime', 'MEMBER') THEN
			EXECUTE format('GRANT faas_runtime TO %I', l);
		END IF;
		IF NOT pg_has_role(l, 'faas_node_logins', 'MEMBER') THEN
			EXECUTE format('GRANT faas_node_logins TO %I', l);
		END IF;
		INSERT INTO public.compute_node_db_logins (login, node_name) VALUES (l, node)
			ON CONFLICT (login) DO NOTHING;
	END LOOP;
END
$$;
