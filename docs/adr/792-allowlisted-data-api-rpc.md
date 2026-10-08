# ADR-792: Allowlisted Data API functions

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Extend ADR-650's relation-only preview with authenticated POST RPC
  routes for explicitly annotated, executable PostgreSQL security-invoker
  functions. Keep database execution inside the customer workload.
- **Why:** Multi-table operations need one database transaction; separate HTTP
  writes cannot provide atomicity. The existing PostgREST client already exposes
  typed `rpc()` calls.
- **Consequences:** The restricted binding reads the callable contract at fresh
  runtime startup and during private type export. Exact function comments
  `@gregale:rpc`, schema usage and execution privileges are required. Overloads,
  security-definer functions, unnamed arguments, variadic and OUT/INOUT
  signatures, unsupported types and non-set composite returns are excluded.
  Supported results are known scalars, void, scalar sets and sets of exposed
  relation rows. Defaults make arguments optional; PostgreSQL arguments and
  scalar results remain nullable. Existing catalog, output, body, row and query
  limits bound this work; no quota or credential mode is added.
- **Authorization:** Application JWT verification and fixed SQL-role rewriting
  apply before forwarding. Function statements execute under the same binding
  privileges and RLS as CRUD. Functions are owner-authored application code;
  invoker status does not prove their bodies safe. Owners must review bodies,
  use schema-qualified names and restrict grants. The gateway does not permit
  GET/HEAD RPC, arbitrary SQL, transactions across requests or role selection.
- **Lifecycle:** Refresh via a fresh restart after function signature, comment,
  body-security or grant changes. This replaces both the gateway allowlist and
  PostgREST cache. Type export does not refresh either. SQL grants still apply
  immediately; removing an annotation alone requires a restart. Apply schema
  compatibility rules to function arguments and results as well.
- **Validation:** Local PostgreSQL/PostgREST acceptance covers opt-in rejection,
  restricted execution, typed arguments/results, two-user isolation and rollback
  after SQL constraints and RLS rejection. Native/provider and staging evidence
  remain independent rollout prerequisites.
