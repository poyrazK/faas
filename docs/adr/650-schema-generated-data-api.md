# ADR-650 · Schema-generated Data APIs and application clients

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Package pinned PostgREST with a JWT-verifying Node gateway as an ordinary Gregale app. Add the portable `data_api` managed PostgreSQL credential mode, a private catalog-based TypeScript generator, `gregale data-api create|types|refresh`, and `@gregale/data`.
- **Why:** Application table endpoints and their TypeScript definitions currently duplicate database schema knowledge. PostgREST supplies schema-driven CRUD and OpenAPI while Gregale's existing app, binding, build, deployment and task lifecycles supply ownership and execution.
- **Consequences:** An explicitly opted-in and newly qualified backend can provision schema-restricted bindings. No additional resource owner, deployment path, metering model or plan quota is introduced. Applications and bindings use existing quota and egress admission. Runtime and export limits are canonical in `pkg/api/limits.go` with an embedded-runtime parity test.
- **Rejected alternatives:** Generating SQL endpoints inside apid would move customer query execution into the control plane. Sending database passwords to client machines would weaken binding custody. Classic PostgREST administrator role membership would allow a JWT role-selection mistake to become DDL authority. Public schema-introspection endpoints would expose an owner development tool to application clients.

The `api` schema belongs to the stable migration owner. A Data API login has
no memberships, ownership, DDL, role creation or RLS bypass, and receives only
schema usage, table DML and sequence privileges in `api`, including future
objects created by that migration owner. Provisioning rejects conflicting schema
ownership, missing grants, public-table access and executable security-definer
functions. Migration bindings retain their release-task-only delivery boundary.
Credential rotation and retirement use the existing binding generation workflow.

The gateway validates application JWTs against an explicit HTTPS issuer/JWKS,
audience and RS256/ES256 algorithm allowlist. It preserves `sub` for RLS and
overwrites the SQL role with the exact binding login. An internal random secret
signs tokens used only on the loopback PostgREST port. Application account keys
do not authenticate data access. V1 exposes relation CRUD and authenticated
OpenAPI, and excludes RPC routes. Customer migrations must define RLS and safe
views; PostgreSQL permissions and policies are the authorization authority.

Type generation runs as an owner-authorized, deployment-attached manual task
using the serving binding. Fixed, parameterized PostgreSQL catalog queries run
inside the customer workload under a read-only repeatable-read transaction,
never against Gregale's catalog or inside apid. This customer-database exception
to the catalog sqlc rule follows the existing provider credential utility SQL
boundary. Output contains only the exposed relation contract and a deterministic
fingerprint, bounded to the existing task output limit. It contains no DSN or
credential. Type export fails on incomplete output and writes atomically.

The schema-cache listener is disabled so an idle app does not keep a managed
database awake through LISTEN. Owners request a fresh app restart after schema
migrations; a snapshot restart could preserve a stale schema cache. SIGUSR1
reload is also forwarded for operator diagnostics. Generated types come from
the current database and `--check` detects drift; exporting does not itself
refresh a serving cache. A failed release does not undo database changes.

Neon advertises this mode only with `data_api_enabled: true`.
Changing this rollout flag requires a fresh qualification report whose
credential capabilities match the advertised contract. It does not change the
placement fingerprint or invalidate existing databases. Advertising the mode
requires live schema isolation,
JWT-sub RLS semantics, password recovery, rotation preservation and revocation
evidence. Existing qualification reports without Data API capability remain
valid for their smaller contract; no production provisioning gate is relaxed.

Acceptance runs real PostgREST and PostgreSQL with JWTs and the TypeScript client.
Native builder/VM deployment and live managed-provider qualification remain
operator rollout checks. The client follows the repository's existing restricted
SDK publication/license policy and is not automatically published.
