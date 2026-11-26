# ADR-20261116213000: Custom Postgres group roles

Status: Accepted (beta since ADR-20261125232048)
Amends: ADR-1027 (customer role attributes)

## Context

A customer tried to run Windmill (1.875) on a Gregale managed Postgres
database and could not finish its first migration:

- With the `read_write` credential: `permission denied for schema public`.
  This is intended; runtime logins do not run DDL.
- With the `migration` credential: `permission denied to create role`. The
  login reported `rolcreaterole = f`, `rolbypassrls = f`, `rolsuper = f`.

Windmill's migrations need two group roles, `windmill_user` and
`windmill_admin`, and `windmill_admin` must be `BYPASSRLS`. Windmill's
self-hosting guide tells managed-Postgres users to run
`init-db-as-superuser.sql` once with an administrative login (the RDS master
user, Neon's `neon_superuser` members, Scaleway's `_rdb_superadmin`), after
which Windmill runs as an ordinary login and only creates the roles when they
are missing. Since PostgreSQL 16, only a superuser or a role that already has
`BYPASSRLS` can create a `BYPASSRLS` role, so `CREATEROLE` alone would not be
enough.

Gregale cannot do this today, by design:

- Every customer login is `NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION
  NOBYPASSRLS` (ADR-1027; `pkg/managedpostgres/neon/credential_roles.go`,
  `pkg/managedpostgres/firecracker/sql.go`).
- No credential mode lets a customer run SQL as an administrator.

The platform already holds the authority it would need. On Neon, Gregale
creates customer logins through its `gregale_owner` login, a Neon API-created
role and therefore a member of `neon_superuser` (`CREATEROLE`, `CREATEDB`,
`BYPASSRLS`). On the native Firecracker backend the platform's admin path runs
as superuser. Other managed providers (RDS, Neon, Supabase, Scaleway) let the
customer do this themselves; Azure Flexible Server cannot.

## Decision

Add **custom group roles**: named, `NOLOGIN` roles a customer declares on a
managed Postgres database and grants to that database's own Gregale logins.
The platform creates them through its admin path. Customers never receive an
administrative login.

### Shape

```
gregale postgres roles list    <database>
gregale postgres roles create  <database> <name> [--bypass-rls]
gregale postgres roles grant   <database> <name> --to migration,read_write[,read_only]
gregale postgres roles revoke  <database> <name> --from <access>
gregale postgres roles delete  <database> <name>
```

Windmill's setup becomes:

```
gregale postgres roles create windmill-db windmill_user
gregale postgres roles create windmill-db windmill_admin --bypass-rls
gregale postgres roles grant  windmill-db windmill_user  --to migration,read_write
gregale postgres roles grant  windmill-db windmill_admin --to migration,read_write
```

The first boot then uses the `migration` binding, and later boots use
`read_write`. The feature is generic, so there is no Windmill-specific preset.

### Role contract

- **Always `NOLOGIN`.** A custom role can never authenticate on its own.
- **Attributes.** `NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION
  INHERIT`. `BYPASSRLS` is the only attribute a customer may request.
  `INHERIT` matters only between custom roles (below); it never reaches a
  Gregale login, which is `NOINHERIT` and holds its grants without
  inheritance.
- **Names.** A valid unquoted PostgreSQL identifier (`^[a-z_][a-z0-9_]{0,62}$`).
  Names starting with `gregale_`, `pg_`, `neon_` or `cloudsql` are refused,
  as are `public` and the names of any role that already exists in the cluster
  and is not a custom role of this database.
- **Grants.** Only to the database's own Gregale-issued `migration`,
  `read_write` (session logins too) and `read_only` logins, `WITH INHERIT
  FALSE, SET TRUE`, so the login gains the role only through an explicit
  `SET ROLE`. Grants to `data_api` and `auth` logins are refused. A custom role
  may be a member of other custom roles of the same database, with
  inheritance (`--member-of`): Windmill grants `windmill_user` to
  `windmill_admin`. Cycles are refused, and a role another role is a member
  of cannot be deleted first.
  A grant covers the current and future logins of that access mode: the
  credential `Ensure` path re-applies it on issue and rotation.
- **Ownership.** Objects a custom role creates are owned by it. Deleting the
  role runs `REASSIGN OWNED BY <role> TO <schema owner>; DROP OWNED BY <role>`
  before `DROP ROLE`, and needs the same confirmation as other destructive
  database commands.
- **Limit.** At most 16 custom roles per database.

### `BYPASSRLS` and RLS-dependent features

`BYPASSRLS` skips row-level security policies but never table privileges, so
a custom role cannot read anything its logins could not already be granted.
It does, however, defeat RLS that Gregale features rely on. Therefore:

- A `--bypass-rls` role is refused on a database that has a Data API or Auth
  binding (`gregale_api_*`, `gregale_auth_*` logins), with error
  `postgres_bypass_rls_conflict` naming the binding.
- Enabling Data API or Auth on a database that holds a `BYPASSRLS` custom role
  is refused with the same error naming the role.
- The check and the write share one transaction on the database's control-plane
  row, so a concurrent enable and create cannot both succeed.

### Plans

Custom roles are available on Hobby and above. Free accounts receive the
standard `402 plan_feature_gated` problem. The API is dark behind the
`postgres_custom_roles` platform flag (`403 postgres_custom_roles_unavailable`
while off) and stays out of the public OpenAPI spec and SDKs until launch.

### State and reconciliation

The control plane is the source of truth: a new table
`managed_postgres_custom_roles (database_id, name, bypass_rls, grants[],
state, created_at, created_by)` stores the declared roles and grants. The
provider applies them idempotently and a reconcile pass repairs drift (role
missing, attributes changed, unexpected `LOGIN` or extra attributes, grants
missing). Drift that would widen a role, such as a customer-run `ALTER ROLE`
that the logins cannot perform today, is reported and reset.

Every create, grant, revoke and delete writes an audit event.

### Backends

- **Neon:** run through the existing `gregale_owner` owner session
  (`ownerDSN`, search path pinned to `pg_catalog,pg_temp`). Before creating a
  `BYPASSRLS` role, check that the owner login itself has `BYPASSRLS`;
  projects created before 2023-08-15 lack it and get
  `postgres_bypass_rls_unavailable`. PostgreSQL 16 checks every attribute
  named in `ALTER ROLE` against the caller's own, so an existing role is
  corrected by altering only what differs.
- **Firecracker (ADR-1027):** separate admin batches for the custom-role
  operations accept names validated by this ADR's rules; the login batches
  keep `rolePattern`. A Data API login's fence runs before the login is
  created, so a refusal (SQLSTATE `GRB01`) leaves no login behind.

### Copies, branches and restores

Neon branches and restores copy the role catalog, so a copy keeps the
roles and their table grants, marked with the source's scope. The control
plane does not copy the declaration yet: the customer declares the roles
again on the new database, and the apply adopts the copies (a role marked as
another scope's custom role is re-marked; any other existing role of that
name is refused). Copying declarations with environment clones, PR previews
and restores is a follow-up.

## Consequences

- Windmill, and other self-hosted tools that set up group roles in their
  migrations, can run on Gregale managed Postgres without an external database.
- The baseline "no customer role has `BYPASSRLS`" becomes "no customer login
  has `BYPASSRLS`; a `NOLOGIN` custom role may, only on databases without Data
  API or Auth". Security docs and the db-grants evidence report must say so.
- The platform's admin path gains a customer-named input for the first time.
  Name validation, identifier quoting and the reserved-prefix list are part of
  the security boundary and need dedicated tests.

## Rejected alternatives

- **Hand customers an administrative (`neon_superuser`) login.** It bypasses
  the connection fence, the pinned owner search path and the RLS guarantees of
  Data API and Auth.
- **A fixed `--preset windmill`.** Quicker, but each new application needs its
  own preset. A preset can be added later as a thin layer over this feature.
- **Approval-gated `BYPASSRLS`.** The risk is limited to the customer's own
  data, and the Data API/Auth fence covers the platform features; an approval
  step adds friction without a matching benefit.
- **Allow and warn on Data API/Auth databases.** A warning does not stop an
  application login from silently reading rows the Data API is meant to hide.

## Slices

1. Store, API and plan gate: table, migration, `POST/GET/DELETE
   /v1/postgres/{database}/roles`, grant endpoints, Data API/Auth fence,
   audit events. Behind flag `postgres_custom_roles`, default off.
2. Neon backend: create, grant, revoke, delete through `gregale_owner`;
   re-apply grants in credential `Ensure`; `neon_superuser` `BYPASSRLS` probe;
   reconcile pass.
3. Firecracker backend: admin SQL allowlist and the same operations.
4. Copies: `copyroles`, branches, environment clones, previews and restores
   carry custom roles and re-attach grants.
5. CLI: `gregale postgres roles …`, help and docs (including the Windmill
   guide).
6. Verification: a CI job runs the full Windmill migration on a Neon branch
   with only Gregale-issued credentials, then checks that Windmill starts with
   `read_write`.

## Open questions

- Settled: Windmill 1.875 switches roles with `SET LOCAL ROLE windmill_user`
  / `windmill_admin` on its user transactions, and wraps its own role
  creation in exception handlers, so pre-created roles with `SET`-only grants
  are enough.
- Corrected 2026-11-17: Windmill runs its migrations at every start, but an
  app cannot bind with the `migration` access mode. schedd delivers
  `migration` credentials to release tasks only (`pkg/sched/engine.go`), and
  that boundary stays. Windmill's app instead runs a release command that
  starts the server against `MIGRATION_DATABASE_URL`, waits until
  `/api/version` answers (migrations applied), and stops it; the serving app
  binds `read_write`. See the Windmill guide in `docs/postgres-custom-roles.md`.
- Settled 2026-11-17 (live run on prod, rc.349): Windmill 1.875's migrations
  through the role grants and the `uuid-ossp` extension pass with the
  `migration` login.

## Amendment 1 (2026-11-17): `TEMPORARY` for the schema owner on Neon

The live Windmill run stopped at migration `20260714230440`
(`wrap session gucs in rls policies`), which defines a helper function in
`pg_temp`: `permission denied to create temporary tables`. On Neon the
database belongs to the platform owner, and the schema owner was granted only
`CONNECT, CREATE`. It now also gets `TEMPORARY`.

Only the `migration` login is a member of the schema owner (`NOINHERIT`,
reached through `SET ROLE`), so only migration sessions gain temporary
objects. Runtime, read-only, Data API and Auth logins are not members and
stay without `TEMPORARY`; the credential probes keep asserting that. Native
Firecracker databases are owned by the schema owner, which already holds
`TEMPORARY` implicitly, so the two backends now match.

The grant is applied by `initializeCredentialSchema`, which runs on every
credential issue or rotation and every custom-role apply, so existing Neon
databases pick it up the next time any of those runs.

## Amendment 2 (2026-11-19): default custom roles

With the migrations applied (release command, amendment 1), Windmill served
on the `read_write` login but could not log a user in:

```
new row violates row-level security policy for table "audit_partitioned"
```

Windmill's own connection must bypass row-level security: on a usual install
it owns the tables, or connects as `windmill_admin`. A Gregale login is never
the owner and never `BYPASSRLS`, and it cannot start as `windmill_admin`
through the connection string: Neon's pooler refuses the `role` startup
option ("unsupported startup parameter in options: role"), and session
(direct) bindings are not available on every database.

A role can now be the **default role** of access modes:

```
gregale postgres roles create windmill-db windmill_admin --bypass-rls --member-of windmill_user --default-for read_write
gregale postgres roles grant  windmill-db windmill_admin --to read_write --default
```

- `default_for` lists `read_write` (session logins too) and `read_only`, each
  one the role is granted to. A database has at most one default role per
  access mode (`postgres_custom_role_default_taken`). `migration` is never
  allowed: its login already starts as the schema owner.
- Gregale runs `ALTER ROLE <login> SET role TO <custom role>` on every login
  of those modes, the mechanism that already starts the `migration` login as
  the schema owner. A role-level setting applies to every new server
  connection, pooled or direct, so no connection-string option is needed.
- It adds no capability: the login could already `SET ROLE` to the role in
  any transaction. It changes which role a session starts as. Applications
  can still `RESET ROLE` back to the login.
- Credential `Ensure` sets the default on issue and rotation once the role
  exists, and accepts a login whose only setting is `role=<one of its custom
  roles>`. The custom-role apply sets it on every login of the mode and
  resets it when the declaration drops it, before a role is dropped. Only a
  `role` setting that names a custom role is ever replaced or reset.
- `PUT` without `default_for` keeps the role's current defaults that are
  still granted, so an older CLI's `grant`/`revoke` never clears them.
  Revoking an access mode removes it from `default_for`.
