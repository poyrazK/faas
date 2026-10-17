# ADR-1352 · Restore and publish a measured function migration destination

- **Status:** implementation for qualification; native execution pending; production off
- **Date:** 2026-10-17
- **Decision:** Add a dedicated destination grant, measured restore and durable
  readiness/ownership phases to ADR-1351. Preserve ordinary snapshot clone
  identity rules and the original immutable admission capture and boot history.
- **Why:** Source rollback alone cannot move a SQL backend between hosts. The
  destination needs current authority, verified captured bytes and a successful
  all-worker resource recovery before the scheduler can transfer ownership.

## Permission and physical execution

The authenticated schedd migration RPC selects an immutable measured capture
result recorded by its source vmmd. Capture result publication checks the original
capture permission and occurs before successful source preparation. A fresh
single-destination grant binds the complete source permission and capture, current
application inputs and artifact approval, target node/incarnation, distinct wake,
native request hash, Firecracker version and all captured artifact identities.
Its expiry is bounded by admission TTL, artifact approval and the migration lease.
Exact issuance retries retain the original token and clocks; a different target,
wake, payload or capture cannot replace an outstanding attempt.

A dedicated qualification capability permits this exact migration to retain its
logical instance ID. Ordinary `SnapshotRestoreEvidence.Check` continues to reject
same-instance clones. The exception travels through protected input staging,
measured snapshot load and consumption validation as the complete destination
grant. It does not advertise the ordinary production snapshot restore capability.
Memory, VM state and the private drive remain measured, sealed and pinned to the
new physical owner. Kernel/base compatibility and existing egress/app admission
checks remain mandatory. Migration cannot fall back to cold boot.

The native restore executes one resume-hook transport attempt. The Node resource
adapter rebuilds and probes every registered pool before its all-interpreter ACK;
failed or uncertain recovery retires the destination. A successful host result is
retained for duplicate adoption RPCs without loading again or replaying callbacks.
The host execution gate remains closed after readiness until a separate RPC reads
schedd's committed ownership. Activation executes no application callback.

## Durable ownership and rollback

Destination epochs progress from `issued` to `ready`, then `committed` or
`retired`. vmmd writes physical evidence; schedd remains the sole instance-row
writer. Commit locks current source inputs, exact lease and target incarnation,
checks the measured destination receipt and current runtime configuration, then
publishes destination node, wake, network identity, native epoch and configuration
receipt atomically. The original admission capture and boot/promotion records are
immutable. Subsequent native lifecycle checks resolve the committed epoch's
receipt and node-specific input hash. The native consumption validator accepts
only that exact recorded epoch as migration-specific capture authority; ordinary
clones retain their existing catalog checks. Source lease/object cleanup cannot delete
that serving history.

Cancellation fences the original migration token before destination teardown.
Source recovery permission and RUNNING publication remain unavailable until the
recorded destination is confirmed retired. If current policy refuses source
recovery permission, its authenticated original host may confirm lease-pinned
retirement under that rollback fence, without a resume or resource callback. A lost readiness ACK can therefore
leave one pending destination, but cannot authorize another restore or a source
resume alongside it. Lost commit ACKs resolve against the exact durable epoch;
legacy recovery cannot put this source into PARKED. A committed destination is
activated before scheduler acknowledgement retires the source. The existing
expiry reconciler can retry activation and original-source retirement.

Unconfirmed destination cleanup retains its durable capacity claim. Database
admission fences include pending destination RAM, CPU and vCPU alongside resident
instances, independently of service-capacity policy activation and in-memory
scheduler state. Other admissions cannot consume that capacity after a scheduler
restart. New residency and destination issuance serialize before checking pending
rows, including when the first destination claim is still uncommitted. Retirement
and ownership publication release/replace the pending claim.
History cannot be rewritten or discarded through generic lease cleanup.

## Qualification and limits

Portable race tests include `TestFunctionDestinationMem`,
`TestFunctionDestinationPg`, `TestFunctionDestinationGrantScopeAndClone`,
`TestFunctionDestinationHostReadyCommitAndDuplicate`,
`TestFunctionDestinationHostFailuresNeverFallbackOrReplay` and
`TestFunctionDestinationHostCancellationJoinsRestore`. They exercise real
PostgreSQL transactions and simulated native process/consumption boundaries;
these results do not establish a native move or SQL recovery on another host.

Before activation, qualify Node 22 and Node 24 on two supported isolated native
x86_64 KVM hosts with real TLS/SCRAM PostgreSQL. Witness all eight original sessions
closing before capture, eight fresh probed destination sessions before readiness,
closed execution admission until commit, original committed rows, distinct native
process/network ownership and confirmed source retirement. Cover lost capture,
readiness, commit and activation ACKs; failed probes; capture/restore cancellation;
expiry and commit races; capacity pressure; source/destination death and restart;
private-environment authority; and leak acceptance. Held portable callbacks are
not evidence of real vsock packet loss. Unknown physical ownership after vmmd
restart stays quarantined; this change does not reattach a surviving guest.

Custom recovery callbacks may have application side effects. The platform does
not replay a destination attempt, but does not promise globally exactly-once
application effects across an aborted migration and later source recovery. Use
application idempotency or durable jobs for that requirement. No automatic retry
of the interrupted customer invocation is introduced.

`make test-function-migration-native` implements successful migrations and
controlled reply-boundary cases against two prepared native hosts. Both Node
runtimes must execute the baseline, a discarded readiness reply followed by an
explicit identical adoption retry, a completed PostgreSQL ownership commit whose
reply is replaced by an error, and three concurrent identical adoption retries.
The scheduler's existing durable commit resolver handles the commit case. The
fixture issues adoption retransmissions while holding the initial reply; this
does not add automatic adoption retries to the production scheduler, which
conservatively rolls back an adoption error.

The suite uses actual VM RPCs and the production scheduler migration coordinator,
with independent PostgreSQL, persistent all-worker callback counts and physical
host witnesses. These after-effect error injections do not establish real
network packet loss, PostgreSQL failover or guest vsock loss. Version 2 reports
require all eight runtime/scenario pairs and reject old two-case evidence,
changed native epochs, replaced SQL sessions or callback replay. The suite
rejects skipped cases and incomplete cleanup, and always reports the complete
failure matrix and production readiness as unqualified. The fixture and
remaining gates are documented in the
[native migration runbook](../ops/function-migration-native-acceptance.md).
Portable guard tests and compilation of this harness do not establish native
acceptance.
