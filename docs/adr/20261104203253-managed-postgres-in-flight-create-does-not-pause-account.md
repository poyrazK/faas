# ADR-20261104203253: A managed PostgreSQL create in flight does not pause its account

- **Status:** accepted (owner, 2026-11-04: "Exempt in-flight creates")
- **Date:** 2026-11-04
- **Owner:** apid (managed PostgreSQL) maintainers
- **Amends:** [ADR-581](581-managed-postgres-uncertain-accounting-intent.md): "Snapshots
  mark unresolved identities explicitly, so even recorded observations cannot
  make an unknown obligation fresh" now has one exception, an obligation whose
  provider call is still running.

## Context

The production test of managed PostgreSQL on 2026-11-04 created two databases
on one account at the same time. The second create answered
`503 managed_postgres_usage_stale`.

ADR-581 commits an accounting obligation before the worker calls the provider,
and the provider's reply carries the resource's identity. Until the worker
records that identity, the account's usage snapshot marks the obligation
unresolved and admission refuses every create, restore and resize
(`identity_unknown`). The rule exists for a lost reply: the provider may have
created a resource that runs and consumes while the catalog has no identity to
collect it by.

The rule cannot tell that case from an ordinary create whose call is still
running. Every create therefore pauses its account for as long as its own
provider call takes, and a second create in that window is refused.

The H9-P113 fix (production-us hunt #9) already says a live resource reserved in the current
usage window owes no completed window yet, because collection records completed
windows only.

## Decision

1. **A create in flight does not block.** An unresolved obligation does not
   block admission while the resource is `provisioning`, a worker holds its
   lifecycle lease, and it was reserved in the current usage window. Restores
   take the same path.
2. **Everything else still blocks.** The obligation blocks again as
   `identity_unknown` as soon as any of the three stops being true:
   - the reply is lost and the worker releases the lease or dies;
   - the window it was reserved in closes;
   - the resource leaves `provisioning` without an identity.
   Collection then recovers the identity exactly as ADR-581 describes.
3. **One rule for admission and diagnostics.** The exemption lives in
   `UsageProgress.blockingIssues`, which admission and accounting diagnostics
   share (ADR-582). The snapshot carries the lease as `CreationLeaseUntil`,
   set only for `provisioning` rows.

## Consequences

- Parallel creates, restores and resizes on one account no longer wait for each
  other's provider calls.
- A lost reply can leave a resource's usage unaccounted until the lease lapses
  or the window closes, whichever comes first. That is no longer than any new
  database already goes without a reading under H9-P113. Its usage is recovered
  with its identity, and the next admission sees it.
- No schema, API or configuration change. `lease_until` was already in the
  coverage query.

## Rejected alternatives

- **A clearer error only.** Return 409 with `Retry-After` instead of
  `usage_stale`. It is honest, but creates would still happen one at a time for
  no accounting reason.
- **Count unresolved obligations as zero until a deadline.** A fixed provisioning
  deadline outlives a worker that died, while the lease does not.

## Tests

`TestAccountingInFlightCreationBoundaries` covers a held lease, a lapsed or
missing lease, a reservation in the previous window and a tombstone, and checks
diagnostics and admission agree.
`TestUncertainProvisionAdmitsWhileTheCreateIsInFlight` (memory and Postgres
stores) admits during the provider call, refuses at the lease's end and at the
window's end, and refuses after the reply is lost.
