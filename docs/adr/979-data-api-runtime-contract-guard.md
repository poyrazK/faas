# ADR-979: Opt-in runtime contract guards for the Data API

- **Status:** accepted
- **Date:** 2026-10-11
- **Decision:** Add an optional `expectedSchemaFingerprint` to the existing Data
  API SDK and export `schemaFingerprint` from generated TypeScript. Guarded
  requests use `/__gregale/contract/<fingerprint>/rest/v1/...`. The runtime
  authenticates the application token and compares the expected fingerprint
  with its immutable startup contract before forwarding to PostgREST.
- **Why:** Generated application types can outlive their serving deployment.
  Rejecting a mismatch before SQL runs also protects mutations. A dedicated
  route is required: older runtimes reject it instead of silently ignoring a
  new request header and executing a write. Clients never fall back to the
  unguarded route and do not automatically replay rejected mutations.
- **Consequences:** Exact matching is opt-in and rejects additive changes as
  well as breaking changes. HTTP 409 `schema_contract_mismatch` identifies a
  mismatch; an unavailable serving fingerprint returns HTTP 503. Older runtimes
  return HTTP 404. Regeneration and coordinated runtime refresh remain required.

The guard retains external JWT verification, restricted SQL-role selection,
RLS, grants, method restrictions, browser CORS and existing query/body limits.
It requires no preflight schema fetch or database query, and does not change
the normalized snapshot format or the fingerprint calculation. Guarded OpenAPI
documents keep the qualified base path. Request diagnostics retain fixed
rest/RPC categories and error codes without logging fingerprints or data.

This matches the captured serving contract, not a fresh database inspection.
Out-of-band DDL or engine-only cache reloads can make it stale. It is neither
an atomic migration barrier nor proof of behavioral or authorization
compatibility. Use the existing sync/restart and verification workflow after
schema changes. No new service, quota, public management API or VM lifecycle
owner is introduced.
