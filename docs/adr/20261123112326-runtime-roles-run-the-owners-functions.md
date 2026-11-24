# ADR-20261123112326: Runtime logins and custom roles run the schema owner's functions

Status: Accepted
Amends: ADR-1027 (PUBLIC privileges), ADR-20261116213000 (custom Postgres group roles)

## Context

Both managed PostgreSQL drivers revoke the default `EXECUTE` to PUBLIC on
functions the schema owner creates
(`ALTER DEFAULT PRIVILEGES FOR ROLE <owner> REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC`
in `pkg/managedpostgres/neon/credential_roles.go` and
`pkg/managedpostgres/firecracker/sql.go`). Runtime logins get table and
sequence grants only. ADR-1027 expected apps to grant `EXECUTE ... TO PUBLIC`
in their migrations.

Software written for stock PostgreSQL never does that, because PUBLIC can
already execute every new function. On prod (2026-11-23) Windmill 1.875
migrated cleanly through a release command, logged in, and then failed every
workspace request with `permission denied for function set_session_context`:
its `user_db` transactions run as `windmill_admin` (the read_write login's
default custom role) and call a function migration 20250412144540 defines
without any grant. The same happens to any app whose migrations define a
function (a plpgsql helper, a stored procedure, an RPC-style function) and
whose runtime connection then calls it.

Restoring PUBLIC's `EXECUTE` is not an option: Data API and Auth logins and
read-only logins must not reach the schema owner's functions, SECURITY DEFINER
ones in particular (their issuance checks refuse that).

## Decision

Grant `EXECUTE` on the schema owner's routines in schema `public` to exactly
the roles an app's own code runs as:

- `read_write` logins (and `session` logins, which carry read_write privileges):
  `GRANT EXECUTE ON ALL ROUTINES IN SCHEMA public` plus
  `ALTER DEFAULT PRIVILEGES FOR ROLE <owner> IN SCHEMA public GRANT EXECUTE ON FUNCTIONS`.
  Issued with the table and sequence grants every time the login is ensured.
- Every declared custom role (ADR-20261116213000): the same two grants, each
  time the declaration is applied. Custom-role memberships are SET-only, so the
  role itself needs the privilege; dropping the role (`DROP OWNED`) removes
  both grants.

PUBLIC keeps no `EXECUTE`. Read-only, Data API and Auth logins are unchanged,
as are their SECURITY DEFINER issuance checks. Migration logins already act as
the owner.

## Consequences

- Apps, and tools such as Windmill, call the functions their migrations make
  without extra grants. Windmill no longer needs a `GRANT EXECUTE` step in its
  release command.
- A `read_write` login can run the owner's SECURITY DEFINER functions in
  `public`, which run as the schema owner. Only the customer's migrations can
  create those, so this runs code the customer wrote with the privileges the
  customer gave it; the runtime login still cannot run DDL itself.
- Like table grants, these are reapplied when a login is ensured or the custom
  roles are applied, so a migration that revokes `EXECUTE` from the runtime
  login on a function in `public` is undone at the next issuance. Apps that
  need a function hidden from the runtime login put it in another schema.
- Existing databases pick the grants up the next time their credentials are
  ensured (issuance, rotation, rebinding) or their custom roles are applied.
