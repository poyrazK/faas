# ADR-565: Recover managed PostgreSQL usage across the fleet before corrections

- **Status:** accepted
- **Date:** 2026-10-04
- **Decision:** Discover the ready database fleet through existing keyset
  pagination, prepare work from durable coverage, and recover one missing window
  per eligible database per round. Finish the recovery rounds before replaying
  any corrections, also one window per database per round. Order work by its
  oldest successful observation, preserving catalog order for ties.
- **Why:** Per-database recovery priority is insufficient when earlier,
  already-covered databases replay corrections before a later unmetered
  database. A deterministic 50-request quota simulation repeatedly exhausted
  capacity before that database received any recovery. A long backlog on one
  database can similarly consume capacity before others receive a single turn.
- **Consequences:** Unmetered databases receive priority over previously
  observed ones. Successful observations persist in coverage, so reconstructing
  a collector preserves the preference for work that has not made progress.
  Each sweep retains one work item per discovered database in memory; discovery
  remains paginated. The existing combined ceiling of 24 requests per database,
  three-window correction horizon, contiguous recovery, transactional replacement,
  and shared restore-root accounting remain unchanged. Failed work is deferred
  once per database without stopping other databases. Cancellation stops further
  requests and sweep completion includes the final database outcomes. No schema,
  provider placement, customer quota, or VM lifecycle changes are required.
- **Rejected alternatives:** Replaying each database immediately after its own
  recovery preserves fleet starvation. Finishing all 24 recovery windows for one
  database before visiting another gives large backlogs disproportionate access.
  A local rotating cursor loses ordering on restart. Treating a process-local
  request ceiling as a provider-account budget would misrepresent capacity shared
  by other backends and processes.

This decision extends [ADR-516](516-managed-postgres-usage-correction-replay.md).
It provides scheduling priority within one sweep, not shared rate admission or
an unconditional starvation guarantee. A repeatedly failing provider request
does not update successful coverage and can retain its ordering position.
Explicit provider-account identity, durable attempt scheduling, and coordinated
request pacing remain open. Provider-instance cooldowns from
[ADR-500](500-managed-postgres-provider-rate-limit-cooldowns.md) still apply.

Deletion settlement also remains open: lifecycle tombstones currently disappear
from collection and freshness even when windows are missing. Fixing it requires
durable accounting targets through shutdown, qualification of retained provider
history, and reconciliation of unavailable history and delayed corrections.
Neither a successful shutdown nor a missing consumption response proves zero
usage. Fleet scheduling must not be described as closing that admission gap.
