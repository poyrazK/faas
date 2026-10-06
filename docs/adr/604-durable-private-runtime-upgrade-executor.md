# ADR-604: Durable private apid runtime upgrade execution

Status: accepted · 2026-10-06

## Context

ADR-603 enforces first traffic activation, but separate caller checkpoints can
lose a committed build or traffic write. A restarted updater must not queue a
second build, adopt new inputs or reactivate a deployment after rollback.

## Decision

Add a private `RuntimeUpgradeOperationStore` and apid `runtimeupgrade.Executor`.
No customer route, CLI or daemon startup enables these seams. Customer previews
retain `execution_available=false`. The caller supplies a stable operation UUID,
owned app/account, pre-uploaded pending explicit zero-traffic candidate, exact
serving deployment, source checksum, target release and qualification report
digest. This slice does not create the candidate or copy a source archive;
private admission must reserve and stage those first. Builderd still verifies
the retained source bytes. Reject job/service execution, canary, sidecar and
environment graph shapes through the existing preparation fences.

Registration locks the original environment, active app, deployments and
workload specs using ADR-603's read-committed parent fences. Lock qualification
for share and require the exact unrevoked report. Retain the target pin, reviewed
baseline and operation in one transaction, without a queued build. No worker
can adopt a later configuration/secret revision. Identical registration returns
the retained operation, including terminal history; a changed intent conflicts.
Allow one operation per candidate and at most one active upgrade per app.

The journal has `prepared`, `waiting`, `complete` and `blocked` phases. It stores
identities, hashes, bounded timestamps and fixed blocker codes, never exception
text or secret values. Operation UUID is also the stable build UUID. Terminal
phases and reviewed intent cannot be updated; SQL completion requires a matching
immutable cutover record. Treat operation rows as operational clone data, never
clone/retry input. Candidate deletion cascades the journal. Keep it through
binary/schema rollback; this is within the trusted control-plane database
boundary, not protection against a privileged SQL operator.

Claims prioritize the oldest due attempt, then creation time and ID, using
`FOR UPDATE SKIP LOCKED`, a new UUID token and a 30-second lease. Waiting
candidates cannot monopolize each worker tick ahead of other due apps. The
advance transaction locks the operation, checks the current token
and expiry, then acquires the preparation/traffic fences. The final checkpoint
checks lease expiry against the database clock after lock waits. Failure or
expiry rolls back every effect; a previous holder cannot publish a checkpoint
after takeover. MemStore uses its mutex and checks the lease before effects.

In `prepared`, recheck current input fingerprints, pin, candidate shape and
qualification. Queue exactly the operation's build and advance to `waiting` in
the same transaction, including the existing `build_queued` notification.
Builderd's durable queue polling recovers missed notifications. Builderd,
imaged and schedd own their normal build, image and fresh candidate prime;
apid never calls a VM or writes instance state. Waiting requires this exact
build, successful build completion and a live explicit zero-weight candidate.
Incomplete readiness stays waiting; changed or expired evidence blocks.

When readiness exists, use ADR-603's cutover helper inside the operation
transaction with the exact wake and reviewed qualification report. Commit
traffic, immutable cutover, traffic notification and `complete` together. If a
matching historical cutover already exists, acknowledge it without traffic
changes, including after rollback, later revocation or the operation deadline.
Completion is historical activation, not current routing, gateway acknowledgment,
drain, continuous health or retirement proof.

Poll due operations every five seconds, with a 30-minute deadline from
registration checked after blocking fences. Lease loss/DB errors leave the
claim recoverable on expiry; infrastructure failures stop this executor
invocation and are retried by restarting it. Expected drift becomes a terminal
fixed blocker without changing serving traffic. A blocked preparation remains
zero-weight for inspection; no automatic cancellation, cleanup, replacement
candidate, baseline refresh or new acceptance is performed. A new reviewed
candidate is required for another operation. These bounds live in
`pkg/api/limits.go`.

## Consequences

Private operation execution can resume after lost responses and process death
without duplicated queue admission or repeated activation. Customer source
reservation/staging, authenticated apply/status/cancel controls, worker startup
and supervision, maintenance scheduling, gateway convergence/drain barriers and
dedicated native end-to-end acceptance remain outside this slice.

## Validation

Memory and real PostgreSQL contracts cover atomic preparation, immutable intent,
account ownership, concurrent claims, elapsed lease takeover, response loss after
queue and cutover, incomplete readiness, successful completion and rollback
history. PostgreSQL tests inject checkpoint failures after queue/traffic writes
and require full rollback; expire a lease during an app-lock wait; age a private
synthetic deadline; reject raw intent changes and completion without a cutover.
The migrated clone-schema coverage includes the operation table. Synthetic
source/build/VM observations do not qualify a runtime. Spec §14 / STATUS still
require dedicated native Linux amd64 KVM acceptance and final leakcheck before
enabling the updater for customers.
