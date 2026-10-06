# ADR-603: Atomic activation of qualified runtime upgrade candidates

Status: accepted · 2026-10-06

## Context

ADRs 597–602 retain the selected runtime, source, serving baseline, native
qualification and fresh candidate cold-boot acceptance. Those read validators
cannot authorize traffic safely: inputs or qualification can change before a
separate traffic transaction. Legacy traffic, canary, rollout and failure
writers can also give a zero-weight candidate its first traffic.

## Decision

Add the private apid-owned `RuntimeUpgradeCutoverStore` seam. No HTTP, CLI,
console, scheduler or collector calls its mutation method. Keep customer
`execution_available=false`. The caller supplies account/app/candidate identity,
the reviewed serving deployment, exact target, acceptance wake and qualification
report digest. Missing or changed intent never selects a weaker path.

Require a live, explicit zero-traffic candidate with its completed pipeline,
exact source/runtime/artifact binding, unchanged retained serving and input
fingerprints, current guest configuration and secret-version fingerprints,
unrevoked target qualification and the same immutable acceptance. Apply the
central 15-minute acceptance limit from cold-boot dispatch. Readiness is the
existing trusted schedd/vmmd acknowledgment, not a new native attestation or
continuous health guarantee. Reject canaries, held environment graph workloads,
service rollouts and mismatched scopes through the existing baseline/acceptance
validators.

Use one PostgreSQL read-committed write transaction. Resolve the original
environment owner, lock that environment for share, then lock the active app
and its deployments in ID order. Retain workload spec share locks and recheck
the owner after acquiring locks. Reread configuration after any lock wait:
publication triggers serialize child edits on parents without necessarily
updating the parent tuple, so an early repeatable-read snapshot would be stale.
Retain the qualification row share lock through commit, ordering revocation
with activation. Existing deployment-first legacy writers can conflict or
deadlock with parent-first cutover; aborts remain conflicts and never authorize
partial traffic. MemStore performs the same gate under its mutex.

After all checks and artifact retention locks, retain an immutable cutover
record and change precisely the candidate from 0 to 100 and the reviewed serving
deployment from 100 to 0. Keep both live and preserve the serving artifact for
existing rollback. Check both expected rows were updated. Publish the existing
`deployment_changed` traffic notification within the PostgreSQL transaction;
failure rolls back receipt, weights and notification together. This does not
claim gateway acknowledgment or request-drain completion.

The cutover record binds candidate, previous serving deployment, target,
acceptance wake, qualification report and time. Identical retries return this
history without rerunning activation, including after a rollback or later
revocation. Changed retries conflict. A historical record is not current
traffic or a new application of the runtime upgrade. Subsequent ordinary
traffic/rollback operations on an already activated artifact retain their
existing semantics; this gate protects its first activation.

Install a deployment-table trigger requiring a matching cutover record before
any pinned candidate receives positive live traffic. This covers all SQL
writers, including legacy direct SQL, redistribution, canary/service promotion,
rollback and older binaries. Match the accepted physical layer, pinned release,
wake and report; replacing a serving upgraded layer cannot borrow its record.
Pin insertion also locks the deployment and rejects an already serving row,
closing the inverse write order. Map these named fences to `ErrConflict`.
Installation refuses an already serving
pinned preparation rather than changing customer traffic or inventing evidence.
The ledger and ordinary state seams share the trusted control-plane database
boundary; this is not protection against a privileged operator forging rows or
disabling triggers.

MemStore validates proposed generic weights before mutating any row and fences
MarkLive/status transitions and serving layer changes. Its canary/recovery and
service orchestration conservatively refuse operations while an unactivated
runtime candidate is among live predecessor choices. Failure fallback excludes
unactivated candidates in both stores, and automatic in-memory rollback also
excludes them. PostgreSQL's table fence rejects any other writer that attempts
their activation. Ordinary applications without runtime pins retain existing
behavior. Preparing an upgrade beside a newly started unrelated rollout can
therefore require discarding the upgrade candidate first.

Treat cutover records as operational clone data. Never copy them to retries or
environment clones. Instance cleanup does not erase them; candidate deletion
cascades them. Retain records and traffic fences through schema rollback.

## Consequences

The state layer now has atomic activation enforcement. It does not introduce
customer apply, maintenance scheduling, an apid operation executor, gateway
acknowledgment, drain barriers, a signed VM retirement receipt or a native
end-to-end acceptance result. Those remain explicit follow-up work before
enabling runtime updates for customers.

## Validation

Memory and real PostgreSQL contract tests exercise the direct cutover, retained
rollback, historical and concurrent retry, expected identities, input/artifact
drift, missing/expired acceptance and qualification revocation. Deterministic
PostgreSQL lock tests commit configuration edits and revocation while cutover
waits and require it to reread and reject. Injected traffic-write failure must
roll back both authorization and weights. Generic direct/indirect traffic,
canary/recovery attempts, raw SQL bypass, immutability, instance cleanup, parent
cleanup and clone-schema coverage are tested. All VM/native observations in
unit fixtures are synthetic. Spec §14 / STATUS deployment acceptance still
requires dedicated native Linux amd64 test-metal and final leakcheck.
