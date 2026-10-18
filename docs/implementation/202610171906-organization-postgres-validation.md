# Organization PostgreSQL validation

Local evidence for [ADR-202610171906](../adr/202610171906-organization-managed-postgres-ownership.md),
2026-10-17. These checks use PostgreSQL 16 with TLS enabled and Go 1.26.9.

Passed checks:

- The complete `pkg/managedpostgres` suite with race detection, including real
  SQL credential delivery and durable reconciliation. After the final memory
  lock-order change, focused ownership and memory promotion regressions also pass.
- Ownership migration backfill from app bindings and restore ancestry, rejection
  of mixed roots, immutable database ownership and distinct binding namespaces.
- Atomic company quota admission across creator and company namespaces; personal
  quota isolation; retained company usage and costs after creator erasure without
  a remaining company app.
- Independent memory and PostgreSQL catalogue fences against offboarding, and
  memory promotion/membership lock ordering.
- Migration schema isolation, issued-source adaptation and session-scoped direct
  connection routing with race detection.
- Memory/PostgreSQL organization resource and project lifecycle, offboarding,
  billing adoption, runtime admission, erasure and managed-secret policy
  regressions in `pkg/state` with race detection. This run disabled the test
  command's duplicate vet pass after configured static analysis had passed.
- PostgreSQL HTTP acceptance: role/scope isolation, personal export filtering,
  creator erasure, unchanged sealed credentials and usable SQL access, teammate
  deployment, credential rotation, and denial of cached binding replay after
  demotion. Shared project manifest tests cover company database resolution,
  rejection of personal databases and required database credential scopes.
- Canonical schema regeneration, deterministic SQL generation, OpenAPI lint and
  route/schema parity; Go SDK paths; Node client build, Python SDK compilation and selected
  PostgreSQL client regressions; runtime and customer documentation contracts.
- All configured Go static checks across the eleven changed packages. The state
  pass was repeated after a disk-full interruption of its build cache; it then
  reported zero findings.

The broad historical `migrations` test package is not green. Its run failed in
`TestMigrations_00046_APIKeyScopesV2_BackfillAndCheck` against the organization
access trigger introduced by ADR-202610171902, and in
`TestMigration_00074_3_PartialUniqueFires` and
`TestMigration_00074_5_BackfillSynthesizesProjects` against ADR-202610171905's project
ownership trigger. The run then reached the package's ten-minute timeout while
applying migrations for another old test. The new ownership migration's focused
upgrade/backfill/rejection checks pass; a clean historical package run is not
claimed.

No production rollout, native KVM deployment acceptance or native leak-check
result is claimed. This environment has no `/dev/kvm`.
