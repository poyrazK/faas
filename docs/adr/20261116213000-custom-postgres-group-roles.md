# ADR-20261116213000: Custom Postgres group roles

Status: Proposed
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
  NOINHERIT`. `BYPASSRLS` is the only attribute a customer may request.
- **Names.** A valid unquoted PostgreSQL identifier (`^[a-z_][a-z0-9_]{0,62}$`).
  Names starting with `gregale_`, `pg_`, `neon_` or `cloudsql` are refused,
  as are `public` and the names of any role that already exists in the cluster
  and is not a custom role of this database.
- **Grants.** Only to the database's own Gregale-issued `migration`,
  `read_write` and `read_only` logins, `WITH INHERIT FALSE, SET TRUE`, so the
  login gains the role only through an explicit `SET ROLE`. Grants to
  `data_api`, `auth`, `session` or another custom role are refused.
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

Custom roles are available on Hobby and above. Free databases receive
`403 plan_postgres_custom_roles`.

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
  `BYPASSRLS` role, check `rolbypassrls` on `neon_superuser`; projects created
  before 2023-08-15 lack it and get `postgres_bypass_rls_unavailable`.
- **Firecracker (ADR-1027):** extend the admin SQL allowlist from
  `^gregale_(rt|ro|mig)_[0-9a-f]{40}$` to also accept names validated by this
  ADR's rules for the custom-role operations only. Names stay
  `pgx.Identifier`-quoted.

### Copies, branches and restores

Branches, environment clones, PR previews, restores and snapshot copies must
carry custom roles and their grants. Neon branches copy the role catalog
already. `copyroles` already copies role attributes including `bypass_rls`;
it must accept custom roles as known and re-attach their grants to the target
database's logins, which have different names. After any copy, the reconcile
pass runs against the target before its bindings become ready.

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

- Gregale logins are `NOINHERIT` and grants use `INHERIT FALSE`, so a login
  gets a custom role's privileges only after `SET ROLE`. Windmill 1.875 must be
  checked to confirm it either runs `SET ROLE` or needs no inherited
  privileges. Slice 6 settles this before slice 5 ships. If it needs
  inheritance, `INHERIT TRUE` for roles without `BYPASSRLS` is the fallback
  (`BYPASSRLS` itself is never inherited).
- Windmill also creates the `uuid-ossp` extension. It must be on the provider's
  allowed-extension list for the `migration` login.
