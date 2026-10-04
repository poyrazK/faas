# ADR-461 · Managed PostgreSQL credential privileges

- **Status:** accepted for gated preview implementation; live Neon qualification required
- **Date:** 2026-10-01
- **Decision:** issue SQL-created runtime and migration logins, with stable schema ownership, instead of app-facing Neon administrator roles.
- **Why:** API-created Neon roles inherit `neon_superuser`. App bindings need data access without role administration, database creation, replication, or RLS bypass.
- **Consequences:** `read_write` grants public-schema DML and sequence usage. Explicit `migration` bindings use direct connections and a non-login schema owner. Rotation revokes logins without deleting schema objects. Existing administrator bindings require deliberate rotation; no credential is replaced automatically.
- **Rejected alternatives:** permission labels on API-created roles do not change PostgreSQL grants. Deriving passwords from an API key couples app credentials to operator API-key rotation. A separate credential vault duplicates the existing sealed-secret boundary and Neon's password storage.

SQL role creation uses a random password once, a deterministic name, and an
ownership marker. Retries verify the existing role and retrieve its password
through the Neon API without resetting it. Neon forwards SQL role/password
changes to its control plane; projects already require `store_passwords=true`.
External PostgreSQL utility DDL requires quoted identifiers. The adapter
derives role names from hashes, quotes them with `pgx.Identifier`, and binds
ordinary query values. Gregale catalog queries continue to use sqlc.
If credentials cannot be recovered or their privileges do not match the
requested mode, issuance fails before a secret is delivered.

A non-login owner role owns `public` and objects created by migration logins.
Migration sessions automatically set that role. Both login modes lack
administrator attributes; runtime roles have no role memberships and cannot
create schema objects. Grants cover existing public tables/sequences and future
objects created by the schema owner. Public schema creation, temporary object
creation, and public execution of managed owner functions are revoked. Custom
schemas and function execution require deliberate migration-owner grants.

Existing object ownership is preserved. Adoption of an older preview database
may require an operator to transfer application objects to the schema owner
before migrations can alter them. Revocation refuses to drop a login that owns
objects, disables login, terminates its sessions, and removes its grants before
role deletion. Legacy API-created identities remain recoverably revocable.

A restored branch disables inherited managed logins before becoming ready.
New target bindings receive identities scoped to that branch. Qualification
must verify runtime permissions, RLS enforcement, migration ownership, and
credential retirement; older approvals cannot authorize the new contract.

Sources: [Neon roles](https://neon.com/docs/manage/roles),
[Neon database access](https://neon.com/docs/manage/database-access),
[SQL role forwarding](https://github.com/neondatabase/neon/blob/main/pgxn/neon/neon_ddl_handler.c),
[PostgreSQL role grants](https://www.postgresql.org/docs/current/sql-grant.html).
