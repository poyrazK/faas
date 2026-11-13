# Postgres runtime roles

Daemons can log in as their own member of `faas_runtime` instead of the schema
owner `faas` ([ADR 20261023104734](../adr/20261023104734-database-runtime-roles.md)).
A runtime role reads and writes rows. It cannot change, remove or empty a
table in one statement, pass a privilege on, or write the migration ledger.

## What converges automatically

Every postgres-role run (bootstrap and control-plane converge) creates the
roles and grants on the primary. Nothing uses them until a unit opts in.

Check the result on the control plane:

```sh
sudo -u postgres psql -d faas -c "\du faas_*"
sudo -u postgres psql -d faas -Atc "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'public' AND c.relkind IN ('r','p') AND NOT has_table_privilege('faas_runtime', c.oid, 'DELETE')"
```

The second query prints `0` once every table carries the grant. Migrations
applied later inherit it through default privileges.

## Move the control-plane daemons

1. Apply pending migrations first (`migrate` as root, the normal release data
   step). A runtime role cannot migrate; a daemon that finds pending migrations
   refuses to start and names the first one.
2. Set `faas_postgres_runtime_roles_enabled: true` for the control-plane host
   and run the control-plane converge. From CD, set the environment
   **variable** `POSTGRES_RUNTIME_ROLES_ENABLED=true` instead
   (`scripts/ci/postgres_runtime_roles_vars.sh` stages it). Before touching any unit it logs in as
   each role from that role's OS user and stops on the first failure, so a
   missing ident line or role never reaches a daemon. It also stops if
   `/etc/faas/compute-db.env` on that host carries a password DSN, because
   `FAAS_DB_ROLE` replaces only the user.
3. The converge writes `zz-faas-db-role.conf` into apid (and its workers),
   schedd, meterd, outboundd, bridged and mcp-bridge, and try-restarts the
   running ones. The `postgres_shared_runtime_role` role then does the same
   for the installed units that run as the shared `faas` OS user
   (gatewayd-public, githubd, s3-gatewayd, maild, agentd, edgednsd), which
   log in as `faas_shared` after it proves that peer login.

Confirm each daemon's sessions:

```sh
sudo -u postgres psql -d faas -c "SELECT usename, application_name, count(*)
  FROM pg_stat_activity WHERE datname = 'faas' GROUP BY 1, 2 ORDER BY 1, 2"
```

No `faas-*` application should still show `usename = faas`. A
`permission denied` in a daemon's journal means it needs a privilege outside
row DML; roll that unit back (below) and record the statement for the ADR.

## Move compute nodes

1. Store a 16+ character secret as `faas_postgres_compute_password` (Ansible
   Vault) and run the control-plane converge; it creates `faas_compute`.
2. On each compute node, change the user and password in both DSNs of
   `/etc/faas/compute-db.env` (`DATABASE_URL`, `FAAS_VMMD_DBURL`, and
   `FAAS_DATABASE_URL_DIRECT` when set) to `faas_compute`, then restart the
   compute daemons (vmmd, schedd, imaged, builderd, gatewayd-internal, the
   edge gatewayd-public and s3-gatewayd).
3. Update the CD secret `COMPUTE_DATABASE_URL` to the same `faas_compute` DSN.
   `cd-compute` writes it into a joining node's `compute-db.env` and uses it
   as the runner's `FAAS_PG_DSN`; left on `faas`, the next join brings the
   owner password back.

Fleets with `faas_pgbouncer_enabled` stay on `faas`: a second user is a
second pgbouncer server pool, which the capacity plan does not budget for
yet, and the owner contract refuses them until it does.

## Contract the owner (step 2)

Once every daemon and compute node has run on its runtime role for a soak
(no `permission denied` in the journals), take the owner away from them:

1. Set `faas_postgres_owner_contracted: true` beside
   `faas_postgres_runtime_roles_enabled: true` (from CD: the variable
   `POSTGRES_OWNER_CONTRACT_ENABLED=true`, which needs
   `POSTGRES_RUNTIME_ROLES_ENABLED=true`) and run the control-plane converge.
2. Before changing anything the postgres role lists every session still
   logged in as `faas` and refuses while there is one, naming each by
   application and client address. It also refuses when compute CIDRs are
   configured and `faas_compute` has no password, and on pgbouncer fleets,
   whose server leg still logs in as the owner.
3. It then drops every `faas_map <daemon user> faas` line (only `root faas`
   stays, for migrations and operator tools) and the owner's password lines
   in `pg_hba.conf`, and records `faas-owner-contracted` in the cluster's
   config directory so a node join's pg_hba rewrite keeps them out.

Check that only root can still log in as the owner:

```sh
sudo -u faas psql "postgres:///faas?host=/run/postgresql&user=faas" -c "SELECT 1"   # fails
sudo psql "postgres:///faas?host=/run/postgresql&user=faas" -Atc "SELECT current_user"   # faas
```

A converge that refuses lists what to move; it changes nothing. Because the
check runs at converge time, an operator `psql` session as `faas` left open
also blocks it; close it and converge again.

## Give each compute daemon its own login (step 3)

Compute daemons share `faas_compute` until this is on. To split them:

1. Add the environment **secret** `POSTGRES_COMPUTE_ROLE_KEY` (a random
   string of 32+ characters, for example `openssl rand -hex 32`). It never
   leaves CD: both workflows derive each role's password from it.
2. Set the environment **variable** `POSTGRES_COMPUTE_DAEMON_ROLES_ENABLED=true`.
   This step requires the compute nodes to be on `faas_compute`
   (`COMPUTE_DATABASE_URL` names it); the script refuses an owner DSN.
3. Run the control-plane converge (`cd-controlplane`) first. It creates
   `faas_vmmd`, `faas_node_schedd`, `faas_imaged`, `faas_builderd`,
   `faas_gatewayd_internal` and `faas_gatewayd_edge` from SCRAM verifiers and
   adds them to `faas_runtime`.
4. Then roll the compute nodes (`cd-compute`, or the next `cd-platform`
   release). On each node the join play proves every new login against the
   node's own row, all or none, before installing
   `/etc/faas/secrets/db/<unit>.env` and the `zz-faas-db-role.conf`
   drop-ins. The daemons pick them up at the rollout's restart.

Check on the control plane that each daemon now has its own role:

```sh
sudo -u postgres psql -Atc "SELECT usename, application_name, host(client_addr), count(*)
  FROM pg_stat_activity WHERE datname = 'faas' AND client_addr IS NOT NULL GROUP BY 1, 2, 3 ORDER BY 3, 1"
```

To roll back, set the variable to `false` and roll the compute nodes. The
join play removes the drop-ins and the daemons return to `faas_compute`; the
roles stay, unused. To rotate the key, change the secret and run both
converges in the same order; until the compute rollout, the daemons'
existing sessions keep working and new ones on a not-yet-rolled node fail.
So roll every node right after the control plane.

## Roll back

Undo the contract first: set `faas_postgres_owner_contracted: false` (or the
CD variable to `false`) and converge; the owner lines and ident mappings
return. Then set `faas_postgres_runtime_roles_enabled: false` and converge:
the drop-ins are removed and the daemons restart as `faas`. For a compute
node, restore the `faas` DSN in `compute-db.env`. The roles and grants stay;
they are inert.

## Evidence for per-daemon grants

With daemons on their own roles, `pg_stat_statements` separates their
statements:

```sh
sudo -u postgres psql -d postgres -c "SELECT r.rolname, count(*) AS statements, sum(calls) AS calls
  FROM pg_stat_statements s JOIN pg_roles r ON r.oid = s.userid
  WHERE r.rolname LIKE 'faas\_%' GROUP BY 1 ORDER BY 1"
```

This is the input to the per-daemon grant sets (ADR step 3).
