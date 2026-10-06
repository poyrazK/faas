# ADR-606: Atomic runtime upgrade reservation and private controls

Status: accepted · 2026-10-06

## Context

ADR-605 expects an independently created candidate and captures the reviewed
baseline after source I/O. A failed or slow upload needs an identifiable,
non-executable operation that retains its original inputs, supports retry, and
can be cancelled without a stale executor activating the candidate.

## Decision

Add private apid `Stager.ReserveAndStage` and state
`RuntimeUpgradeReservationStore`. The caller retains operation and candidate
UUIDs, owned app/account, serving deployment, source checksum, target release
and exact qualification report across retries. Bind the destination to an
absolute clean `CandidatePath(operationID)` before reservation. Retain the path
as immutable internal journal metadata; operational clone reset includes it.
No public endpoint, CLI, service unit or deployment enables this seam.
Customer previews continue to report `execution_available=false`.

Reserve in one transaction under ADR-603's environment, app, deployment and
workload parent fences. Copy the serving deployment's immutable source,
handler, scope, customer overrides, instance floor and rollback-on-5xx policy
into a pending explicit zero-traffic candidate. Include the copied rollback
policy in the candidate input fingerprint. Clear prior build/image/rootfs
outputs, mutable Git subscriptions, inferred image evidence, actor identity,
canary state and discovered secret reload signals. Require a deployable account,
known plan/source cap, active managed function, stable live serving deployment,
supported retained source and exact native target qualification. The target pin,
reviewed configuration/secret/input baseline and `reserved` journal commit with
the candidate, or none do. One active operation per app includes reservations.
Concurrent identical reservations return the same retained history after the
app lock; changed intent or destination conflicts. A committed retry performs
no source I/O until it observes the reservation.

`reserved` is not executable. Generic build inserts are fenced against reserved
and terminal operation rows. The existing worker claims a reservation only
when its original 30-minute deadline expires, to retain a fixed
`deadline_exceeded` blocker and release the active-operation slot. Storage
failures leave the same reservation retryable and visible; no new baseline or
candidate is silently adopted. ADR-605 verifies and syncs both configured source
handoffs, then preparation locks the operation before its parent fences and
rechecks the original baseline, destination, candidate, qualification and
deadline. Successful preparation becomes `prepared`; expected drift becomes
`blocked`. Only the existing leased executor queues the stable build.
`PrepareReservedRuntimeUpgradeOperation` is trusted private orchestration; the
database cannot itself verify storage bytes. Legacy pre-uploaded registration
remains available internally, but cannot promote a reserved operation.

Add an account-scoped private `Controls.Status` projection containing operation,
app/candidate/serving/target identities, phase, fixed blocker, timestamps and
whether cancellation is available. It exposes no lease token, spool path,
configuration value, secret envelope or database diagnostics. Status reflects
durable journal state across process restarts, not a gateway convergence or
resource cleanup guarantee.

Cancellation locks the operation before app/deployment pipeline fences. It is
allowed for `reserved`, `prepared` and `waiting`; an identical cancelled retry
returns retained history. In the same transaction, clear the worker lease,
retain terminal `cancelled`, cancel eligible pending/build/image/prime deployment
states, cancel queued release commands or request cooperative cancellation of
running ones, cascade-cancel nonterminal builds, and retain builder VM cleanup
obligations for running builds. Publish notifications to the existing owners in
that transaction; lost notifications cannot erase the cleanup obligation. Apid
never calls schedd or vmmd directly. Cancellation remains available after app
suspension and never grants traffic or rewrites the serving deployment.

A live zero-traffic candidate stays retained; cancellation fences its activation
without manufacturing a deployment cancellation transition forbidden by the
normal live-deployment state machine. Direct cutover publication also checks the
operation phase under the app fence, so fresh acceptance cannot bypass a
cancelled operation. If a matching cutover already committed, acknowledge
historical completion; cancellation never undoes traffic or an operator rollback.
Completed and blocked history cannot be replaced with cancellation. SQL guards
prevent backwards/skipped reservation transitions and changes to terminal
history or immutable reservation inputs.

## Consequences

Private admission can recover slow/failed source preparation, report durable
status and stop unactivated work with fenced cancellation. Published source
handoffs and live zero-traffic candidates require subsequent owner-controlled
retention/cleanup work; cancellation is not proof of immediate VM teardown.
Customer preview approval tokens, public controls, production worker deployment,
gateway acknowledgment/drain, post-cutover health verification and dedicated
native end-to-end acceptance remain outstanding.

## Validation

Memory and real PostgreSQL contracts cover atomic rollback of rejected
reservation, concurrent identical retries, copied overrides, one active app
operation, generic queue/legacy registration bypass rejection, lost reservation
responses, storage retries, account isolation, safe status projection,
configuration and qualification changes during staging, cancellation during
publication, stale worker claims, running-build cleanup retention, fresh live
candidate acceptance after cancellation, historical cutover acknowledgement,
cancellation checkpoint rollback and abandoned reservation deadlines. Existing
executor/source contracts run alongside these checks. Fixtures provide no VM
acceptance evidence; spec §14 and CLAUDE.md still require dedicated Linux amd64
KVM `test-metal` and final `leakcheck` before customer enablement.
