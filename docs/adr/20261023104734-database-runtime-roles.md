# ADR 20261023104734 · Daemons log in as runtime roles, not the schema owner

- **Status:** accepted (2026-10-23)
- **Date:** 2026-10-23
- **Related:** spec §11 (control plane), ADR-127 (sealed env scope),
  ADR-015 (unix-socket auth), ADR-124 F2 (daemons migrate on boot),
  `docs/runbooks/postgres-runtime-roles.md`

## Context

Every daemon connects to Postgres as `faas`, the role that owns the database
and every object in it. Control-plane daemons reach it through the
peer-authenticated socket (`faas_map` maps each daemon OS user to `faas`);
compute nodes use the same role with a password over TCP, and that one DSN in
`/etc/faas/compute-db.env` is shared by vmmd, schedd, imaged, builderd and both
gateways. Migrations grant nothing and no table uses row-level security.

The component-ownership rules in CLAUDE.md ("schedd is the ONLY writer to
`instances`", "apid is the ONLY writer to customer-intent tables") are
therefore conventions. A memory-safety bug or command injection in any daemon,
including the internet-facing gateway or imaged while it parses an untrusted
image, gives the attacker the schema owner: every tenant's rows, sessions, API
key hashes and sealed secrets, plus DDL. As owner it can also drop tables,
disable triggers, rewrite the audit log, mark a migration applied without
running it, and grant itself anything.

Least privilege per daemon is the goal, but the table set each daemon touches
is not written down anywhere, and guessing it wrong takes production down.
`pg_stat_statements` is already enabled (2026-10-21) and records the role of
every statement, which is the evidence a per-daemon grant set needs, but only
once daemons stop sharing one role.

## Decision

Separate the migration role from the roles daemons run as, in three steps.

### 1. Expand (this ADR)

- `faas` stays the owner and the only migration role (`cmd/migrate`,
  `deployctl`/`gregalectl` as root through `faas_map root faas`).
- A NOLOGIN group role `faas_runtime` holds `SELECT, INSERT, UPDATE, DELETE`
  on every table and `USAGE, SELECT, UPDATE` on every sequence in `public`,
  `USAGE` on the schema and `CONNECT` on the database. Nothing is grantable.
  `ALTER DEFAULT PRIVILEGES FOR ROLE faas` gives tables and sequences created by
  later migrations the same grants. It has no `TRUNCATE`, `REFERENCES` or
  `TRIGGER`, owns nothing, and so cannot alter, drop or re-grant an object.
  The migration ledger `goose_db_version` is read-only to it.
- One LOGIN role per daemon OS user, each a member of `faas_runtime`:
  `faas_apid`, `faas_schedd`, `faas_meterd`, `faas_githubd`,
  `faas_outboundd`, `faas_bridged`, and `faas_shared` for the daemons that share
  the `faas` OS user (gateways, realtimed, profiled). They have no password, so
  they log in only through peer authentication on the socket; `faas_map` gains
  one `<os user> <role>` line each, beside the existing line to `faas`.
- `faas_compute` is the password-authenticated member for compute nodes. It
  exists once the operator sets `faas_postgres_compute_password`.
- pg_hba admits `+faas_runtime` on the socket (peer, mapped) and on the
  loopback and compute CIDRs (scram) in both roles that write pg_hba.
- A unit opts in with `FAAS_DB_ROLE=<role>`. `pkg/db` replaces the DSN's user
  with it on both the ordinary and the direct pool. It refuses a role outside
  `faas_<daemon>` and a password DSN for a different user, because a password
  authenticates only its own user.
- `control_plane_service` sets `FAAS_DB_ROLE` on apid (and its workers),
  schedd, meterd, outboundd and bridged when
  `faas_postgres_runtime_roles_enabled` is true (default false). Before it
  moves any unit it proves each role's peer login as that unit's OS user, and
  it stops on a host whose `compute-db.env` carries a password DSN.
- `db.MigrateUp` on a role that does not own the migration ledger holds the
  migration lock, then only checks the schema is current. With pending
  migrations it fails with `ErrMigrationsPendingForRuntimeRole` instead of
  attempting DDL it cannot run. Owner sessions behave as before. Ownership is
  read from `goose_db_version`, not the schema, because `public` belongs to
  `postgres` on clusters created before PostgreSQL 15.
- The ledger-recovery writer check accepts exactly the `faas_runtime` grant
  set and still refuses any other grantee, grantable privilege, `TRUNCATE`,
  schema `CREATE`, or default privilege outside it.

### 2. Contract (follow-up)

Once every unit on a fleet runs on its runtime role and has soaked:

- remove the `faas_map <daemon user> faas` lines, leaving only `root`;
- move compute nodes to `faas_compute` and remove `host faas faas <cidr>`, so
  no compute node can authenticate as the owner; on pgbouncer fleets this also
  maps the pooler's server leg and caps total server connections with
  `max_db_connections`, since a second user means a second pool;
- give the remaining `faas` OS-user daemons their own OS users and roles;
- turn `faas_postgres_runtime_roles_enabled` on by default.

From then on the owner is reachable only from root on the control plane.

### 3. Narrow (follow-up)

Replace each daemon's membership in `faas_runtime` with a grant set derived
from production `pg_stat_statements` grouped by role, one daemon at a time,
starting with the ones on compute nodes. The audit log becomes insert-only for
every runtime role.

## Consequences

- Step 1 changes nothing until an operator opts in; the grants are additive.
- After step 1 an opted-in daemon can still read and write every tenant's
  rows. What it loses is DDL, ownership, `TRUNCATE`, re-granting and the
  migration ledger. Per-table least privilege arrives with step 3.
- A runtime role cannot migrate. Daemons that migrate on boot (imaged, apid's
  clone and runtime-upgrade workers) now depend on `migrate` having run first,
  which the deploy order (data → control plane → edge/node → runtime) already
  guarantees. Out of order, they refuse to start with a message naming the
  first pending migration.
- An operator running ledger recovery with a pre-ADR binary after the grants
  land gets `ErrMigrationRecoverySchema`; use the current binary.
- `schema.sql` is unchanged: `pg_dump --no-privileges` omits grants.

## Alternatives considered

- **Per-daemon grant sets now, derived statically.** Mapping each binary's
  reachable `pkg/state` queries to tables is possible, but raw SQL outside
  sqlc, interface dispatch through the 1,000-method `state.Store`, and
  reflection-driven method retention make the result unprovable. An
  under-grant fails a rarely-run path in production; usage evidence first.
- **Grants in migrations.** Roles are cluster objects that test databases do
  not have, so migrations would need conditional logic and would differ across
  environments. Ansible converges roles and grants as the superuser instead;
  default privileges cover every object migrations create afterwards.
- **Row-level security.** Tenant isolation inside one daemon's queries is a
  separate, much larger change; it does not stop a compromised daemon that
  legitimately serves every tenant.
