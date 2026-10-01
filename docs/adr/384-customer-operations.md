# ADR-384 · Customer operations above execution ledgers

- **Status:** accepted for implementation; not launched
- **Date:** 2026-09-30
- **Owner:** apid, scheduler, gateway, and SDK maintainers

An operation is a customer's logical unit of work. Invocation, Job, AppTask,
and workflow ledgers retain their different execution and recovery semantics.
Operation identity survives supported retries and recovery; execution identity
and attempt history remain inspectable. Delivery success never determines the
business outcome. The implementation starts with ordinary HTTP handlers and
extends through explicit execution adapters, without another privileged daemon.

Definitions capture input/output schemas, verified ownership, progress stages,
handler target, completion destination, and recovery policy. Deployment resolves
source-local schemas into immutable revisions. Admission pins the definition and
deployment/release. Active work retains required artifacts and never silently
changes release. Identity is derived from authenticated ingress, never payloads.

A definition selects exactly one target: an ordinary HTTP method/path or a
named workflow from its owner-scoped deployment. Workflow targets specify a
result step and declared aggregate progress stage. The platform validates and
retains a bounded private DAG snapshot; its content participates in the
immutable operation revision. Customers cannot inject the snapshot. Native
DAG validation preserves waits, dependencies, and exception-route contracts.
Declaring this target does not enable execution: admission stays unavailable
until the controlled workflow dispatcher and recovery contract are qualified.

The controlled workflow admission seam commits a real native run, immutable run
input/DAG, initial step rows, operation projection, backend binding, acceptance
event, scoped submission receipt and private code receipts in one transaction.
It creates no HTTP invocation. Native and operation admission share the app's
workflow active-run quota and advisory lock; that lock precedes code app locks
so native insertion can acquire its app foreign-key lock without a lock cycle.
Public ingress continues to reject this target until the dispatcher is ready.
Native workflow claims exclude the permanent operation marker, and direct
legacy advancement, recovery and cancellation reject it, including after
operation projection GC. Native run retention skips operation-bound history
while recovery can still need its confirmed step results. These admission and
isolation controls do not substitute for the controlled execution adapter.

Workflow coordinators acquire a separate scheduler custody capability, bound
to the operation, real native run, generation and monotonic claim attempt. Only
its digest is stored; it never grants guest reporting authority. Claims and
renewals lock the native run before the operation. A fresh custody read after
the run lock prevents a joined candidate snapshot from stealing a just-renewed
lease. The lease is bounded by `OperationExecutionLeaseMax` (two minutes); the
counter is bounded by `OperationWorkflowClaimsMaxPerRun` (2,147,483,647), matching
the positive PostgreSQL integer representation. Both limits live in
`pkg/api/limits.go`.

Parking releases custody only when no native step is running, preserving an
earlier callback wake. Safe idle expiry or a callback wake obtains a fresh
capability without changing the run identity or erasing confirmed steps. An
expired coordinator with any unresolved running step instead records
`requires_reconciliation`, revokes custody and stops native dispatch without
resetting step evidence. This also applies to suspended tenants. Idle suspended
work remains logically running and parks until tenant activation; a running
event records the pause reason without adding a new business state. These
custody controls still require fenced step execution and instance-bound guest
authority before public workflow admission can be enabled.

The scheduler step mutation seam requires current custody and exact consecutive
native attempts. Resolved input is frozen on the first dispatch and reused for
an already persisted retry. Attempt history and compact step results commit
together; identical terminal receipts preserve their first finished time,
while conflicting receipts are rejected. A released or expired coordinator
cannot change step history. Suspension denies fresh step starts while allowing
a still-owned in-flight step to record its confirmed result. Resolved inputs
and outputs obey the admitted operation value bound; step attempt counters use
`OperationWorkflowStepAttemptsMax` (2,147,483,647), and retained error text uses
`OperationWorkflowStepErrorMaxBytes` (4 KiB), both in `pkg/api/limits.go`.
The coordinator remains responsible for native DAG readiness and recovery
classification. This internal write seam does not itself dispatch handlers,
grant guest authority, settle business outcomes or enable workflow admission.

Admission, idempotency receipt, operation, execution association, and initial
event commit atomically. Keys are scoped to account, app, environment, verified
owner, and operation name. Equivalent JSON inputs replay the original receipt;
different inputs conflict. Duplicate JSON members are rejected. Definition
revision is not part of key scope: a deploy cannot turn a retry into new work.
Deduplication tombstones outlive results for the documented replay window.

Progress is durable, ordered, bounded, and fenced to the active execution
attempt. Guest report authority binds operation, invocation, instance, attempt,
and expiry, in addition to existing workload identity. Client retries carry
report IDs. SSE authorization uses operation ownership; reconnects replay a
durable cursor or explicitly require snapshot resynchronization. PostgreSQL
notifications are wake hints, never the authoritative event log.

The scheduler renews HTTP execution claims while it owns the active wake/handler,
without exceeding the admitted request deadline. Renewal cannot revive an
expired attempt. Losing renewal cancels the live dispatch; a stopped scheduler
leaves ordinary lease recovery to record the configured uncertainty outcome.
The scheduler must persist the selected instance's attempt-bound reporting
authority before invoking an operation handler. A failed binding settles as a
pre-dispatch failure; it cannot run business work with unusable progress authority.

schedd owns execution lifecycle. apid owns customer intent and accepts scoped
reports through narrow transactional methods. Business success, validated result
references, terminal event, and completion outbox commit together. Webhook
retries cannot re-execute work. Disabled/deleted destinations remain a visible
configuration failure. Download access is checked against operation ownership;
durable results contain object references/checksums, not expired signed URLs.

New file attachments copy verified bytes into a unique platform-owned storage
key before the fenced attachment transaction. A durable staging receipt is
reserved before any write; concurrent retries use different keys. Only the
winning report receipt pins a copy to the operation. The copy is private to
apid and shares the configured platform artifact backend, rather than customer
bucket write/delete permissions. Its retention follows the operation's result
horizon, independent of deletion or mutation of the original object. Existing
reference-only records remain readable through their earlier verification path.

Staging, retained and deleting receipts have explicit state constraints and
lease-fenced cleanup. Cleanup receipts deliberately have no cascading owner
foreign keys: owner deletion, recovery and result projection GC must not lose
the only record of external bytes. apid reconciles this bounded queue even while
new admission is disabled. Account byte and object caps count uploads and
deletion retries until physical cleanup succeeds, so an outage cannot turn
failed requests into unbounded retained storage. Checksums remain verified on
download; missing/corrupt storage is an availability error, not business failure.

Uncertain outcomes after dispatch require reconciliation unless the definition
explicitly declares safe repeat execution. Neither timeout nor cancellation
proves that an external side effect did not happen. Recovery requires confirmed
success, confirmed failure, or affirmative authorization of a safe new execution.
Successful workflow steps and Job partitions are reusable only under their
existing adapter contracts. An operation does not imply exactly-once effects.

Each operation generation binds exactly one real backend execution: an HTTP
invocation, workflow run, or Job run. The ledger derives its execution ID and
kind from mutually exclusive backend foreign keys. The current projection must
match the binding's operation, generation, ID and kind. Events and report
receipts reference a binding owned by the same operation. Workflow/Job runs
retain permanent operation markers after projection GC; binding queries also
check their app/account ownership. Backend adapters must lock their execution
row before the common operation row. HTTP writers that predate the common
identity fields retain their invocation identity through generated-column
fallbacks. This schema seam does not itself enable workflow or Job admission.

Active work retains its customer projection and submission key even when an
initial result or identity window has elapsed. Result/event windows reset when
work settles or stops for reconciliation; the full idempotency window is
reserved from that settlement in the same transaction. Event history remains
bounded and may require snapshot resynchronization during a long wait. Once
work is stopped, ordinary projection/tombstone retention can release capacity.
Adapters must also preserve usable deployment/release pins across long waits;
an unlimited timestamp pin is not a substitute for checking active ownership.

Code retention derives from an owned operation and its immutable definition.
Accepted/running work retains its deployment and all verified release members
past timestamp expiry; stopped work retains them only through its result/recovery
window. Cutover and rollback remove weighted traffic while preserving this code.
Private operation cleanup receipts use `customer_operation_code_pins`, separate
from public revision deadlines. Admission, claim renewal, progress, recovery and
settlement never extend the public revision window or the native release-set
deadline. Existing public deadlines are preserved during migration; owned
operation references backfill private receipts for the full release graph.
These references do not bypass public revision-header timestamp validation.
Pin cleanup locks apps before deployments, then rechecks references in a fresh
READ COMMITTED statement after app-lock acquisition. A page considers at most
`RevisionPinCleanupPageMax` (500) deployment IDs, each with at most one public
and one private receipt. Both deadlines must expire, and each receipt present
in the cleanup snapshot must actually be deleted before code is retired. A
concurrent receipt renewal therefore preserves code. Retained references are
excluded before paging. Expiry, owner deletion, and inconsistent admission
metadata release the private reference without an unlimited pin or heartbeat.
Fresh admission and safe retry lock the owned release's apps in ID order before
its deployments, then revalidate every member and release usability. A release
selected by the definition receives the same checks as an explicit request;
locking only the originating app cannot protect another graph member from GC.

All bounds live in pkg/api/limits.go. Qualification covers concurrent admission,
input conflicts, cross-tenant denial, pinned revisions, stale reports, durable
stream replay, retention, artifact authorization, delivery outages, uncertainty,
and cancellation. PostgreSQL/HTTP/SDK acceptance is distinct from native KVM
park/restore, scheduler restart, and leakcheck; both are required for the complete
end-to-end goal. Rollback disables new admission while preserving reads, reports,
delivery, and recovery for already admitted operations.
