# Postgres runtime roles

Daemons can log in as their own member of `faas_runtime` instead of the schema
owner `faas` ([ADR 20261023104734](../adr/20261023104734-database-runtime-roles.md)).
A runtime role reads and writes rows but cannot alter, drop, truncate or
re-grant anything, and cannot write the migration ledger.

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
   and run the control-plane converge. Before touching any unit it logs in as
   each role from that role's OS user and stops on the first failure, so a
   missing ident line or role never reaches a daemon. It also stops if
   `/etc/faas/compute-db.env` on that host carries a password DSN, because
   `FAAS_DB_ROLE` replaces only the user.
3. The converge writes `zz-faas-db-role.conf` into apid (and its workers),
   schedd, meterd, outboundd and bridged, and try-restarts the running ones.

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
   compute daemons.

Fleets with `faas_pgbouncer_enabled` stay on `faas` until the contract step:
a second user is a second pgbouncer server pool, which the capacity plan does
not budget for yet.

## Roll back

Set `faas_postgres_runtime_roles_enabled: false` and converge: the drop-ins are
removed and the daemons restart as `faas`. For a compute node, restore the
`faas` DSN in `compute-db.env`. The roles and grants stay; they are inert.

## Evidence for per-daemon grants

With daemons on their own roles, `pg_stat_statements` separates their
statements:

```sh
sudo -u postgres psql -d postgres -c "SELECT r.rolname, count(*) AS statements, sum(calls) AS calls
  FROM pg_stat_statements s JOIN pg_roles r ON r.oid = s.userid
  WHERE r.rolname LIKE 'faas\_%' GROUP BY 1 ORDER BY 1"
```

This is the input to the per-daemon grant sets (ADR step 3).
