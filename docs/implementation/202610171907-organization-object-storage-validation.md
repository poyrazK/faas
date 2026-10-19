# Organization object storage validation

Local implementation of [ADR-202610171907](../adr/202610171907-organization-object-storage-ownership.md),
2026-10-17, using Go 1.26.9 and PostgreSQL 16. The migration and canonical schema
have been applied to a disposable database; the shared test database is untouched.

Coverage includes company ownership/backfill, mixed-root and finalized-period
rejection, current/historical financial allocation, shared quota admission,
credential revocation and creator erasure. HTTP/S3 acceptance covers unchanged
application credentials and bucket/provider identities, teammate access, stale
signed requests, role/scope boundaries, personal exports, rotation and deployment.
The Go, Node and Python clients expose the reviewed organization routes.

Passed checks:

- Memory/PostgreSQL state regressions with race detection: ownership and legacy
  backfill, quota serialization across namespaces, current/historical billing
  allocation, adoption conflicts, offboarding commit ordering, creator erasure
  and existing personal-storage lock ordering.
- Memory/PostgreSQL HTTP/S3 acceptance against the local provider fixture:
  application and teammate credentials use the same bucket after creator erasure;
  former-member keys and requests revoked during body upload return 403. Bucket
  placement, sealed application credentials and object data remain unchanged.
  Role/scope isolation, personal export filtering, deployment intent, rotation,
  bucket cleanup, signed URLs and storage recovery regressions also pass with
  race detection.
- Component suites for API clients, authorization, account exports, application
  access and PostgreSQL access; the full S3 gateway suite and gateway daemon
  tests, with race detection. Authorization includes all twenty storage
  role/action combinations and the complete action inventory.
- Focused migration upgrade/rollback/backfill and unsafe-root rejection,
  schema isolation and issued-source adaptation, with race detection. The
  disposable schema database was removed; the shared database's public schema
  still has zero application tables.
- Canonical schema regeneration and deterministic SQL generation; embedded
  OpenAPI parity, route/schema compliance and SDK coverage. OpenAPI lint reports
  zero errors, with existing repository-wide warnings.
- The complete Go SDK suite with race detection; Node build and 418 client tests
  plus eight generator postprocessor tests; Python SDK compilation and two
  focused organization-storage client regressions.
- Runtime documentation contracts, ADR numbering, sealed-environment scope,
  text encoding, shell quoting, runbook SQL and durable-entity SDK contracts.
- All configured Go static checks, including vet and security checks, across
  the thirteen changed packages. The export package's focused rerun passes
  after documenting its intentional exclusion of malformed historical events.

The full historical `migrations` suite was not rerun. Its last broad run had the
failures documented in
[ADR-202610171906's validation](202610171906-organization-postgres-validation.md); focused
migration upgrade/rollback/backfill and isolation checks pass for this change.
No production rollout or native KVM deployment/leak-check acceptance is claimed.
