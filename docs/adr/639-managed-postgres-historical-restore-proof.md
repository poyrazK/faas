# ADR-639: Independently mapped historical positions for PostgreSQL restore

- **Status:** accepted
- **Date:** 2026-10-07

## Context

Neon's public branch metadata may report `parent_lsn` while leaving
`parent_timestamp` absent or reporting an earlier, rounded commit time. Live
tests observed a requested `15:43:08Z` returning `15:43:07Z` after initialization.
The existing timestamp-only lineage contract then blocks a legitimate recovery.
Its accepted creation ID establishes custody
(ADR-638), not the point restored. Returning the request timestamp as observed
metadata would defeat the publication gate.

The earlier qualification experiment sent `neon_timestamp` to an ordinary
compute hostname. That connection read the primary's current data. Neon's CLI
routes historical connections through the branch hostname instead:
<https://github.com/neondatabase/neonctl/blob/main/src/commands/connection_string.ts>.

## Decision

Add the optional provider-neutral `RestoreInspector` contract. It receives the
persisted exact target, source, and point; it must inspect without creating
anything. Reconciliation and qualification use it when available, retaining
their independent lineage checks. Providers with timestamp metadata retain
ordinary inspection. No vendor WAL syntax appears in the public product API.

For a settled Neon branch with complete ownership metadata and an absent or
earlier parent timestamp, independently resolve the requested source history:

1. Read the exact source branch and its single primary endpoint; never follow a
   current default. Pin credential retrieval to that branch and endpoint.
2. Require the returned direct hostname to match that endpoint. Replace its
   first DNS label with the source branch ID, following Neon's historical
   routing contract. Require `verify-full` TLS and microsecond-precise input.
3. Read only `pg_is_in_recovery()`, transaction read-only status and the replay
   LSN. A current primary, writable connection, null/zero/malformed LSN, failed
   connection or unavailable history supplies no proof.
4. Compare the independently resolved LSN numerically with actual target
   metadata. Re-read the exact target and recheck its owner, source, creation
   instant, initialization mode, settled state and parent position.

Only this matching mapping permits reporting the verified requested time in
`RestoreLineage`. A timestamp after the request remains a conflict; malformed
metadata supplies no proof. An earlier timestamp alone cannot authorize the
restore: the requested state's independently resolved LSN must match the target.
Missing mapping remains unavailable.
Mapping is re-observed after worker restart and before readiness, without an
in-memory cache or a durable receipt fabricated from request parameters.
Existing atomic readiness/proof receipts retain the verified source and time.

Creation custody remains separate. Its checkpoint precedes mapping, preserving
safe compensation even when historical SQL or lineage validation fails. This
change does not close an upstream commit before lost creation acknowledgement.

Qualification version 7 requires exact source identity, correct lineage on
creation and readiness, and replay of the same restore with unchanged target
and lineage, in addition to actual restored data and credential isolation.
Older approvals cannot authorize new provisioning under this contract. Health
observation remains read-only and does not open historical SQL connections.

## Validation and limits

Regression tests cover missing and contradictory proof, numeric LSN comparison,
route pinning, lost acknowledgement, custody before verification, fresh-adapter
recovery, changed metadata, and reconciliation/qualification gates. Ordinary
PostgreSQL must not impersonate a historical Neon recovery connection.

Live acceptance requires disposable Neon resources: normal recovery, durable
receipt restart and lost-response recovery, transaction-boundary data checks,
login isolation, a single physical target per intent, and confirmed cleanup.
Source history must remain available during provisioning. Snapshot retention,
snapshot native copy and completed settled usage remain separate qualification
requirements; this decision does not enable production provisioning.
