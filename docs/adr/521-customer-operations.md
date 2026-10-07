# ADR-521 · Customer operations above execution ledgers

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

Admission, idempotency receipt, operation, execution association, and initial
event commit atomically. Keys are scoped to account, app, environment, verified
owner, and operation name. Equivalent JSON inputs replay the original receipt;
different inputs conflict. Duplicate JSON members are rejected. Definition
revision is not part of key scope: a deploy cannot turn a retry into new work.
Deduplication tombstones outlive results for the documented replay window.

Progress is durable, ordered, bounded, and fenced to the active execution
attempt. Guest report authority binds operation, the real execution, instance,
attempt and expiry, in addition to existing workload identity. Client retries carry
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

All bounds live in pkg/api/limits.go. Qualification covers concurrent admission,
input conflicts, cross-tenant denial, pinned revisions, stale reports, durable
stream replay, retention, artifact authorization, delivery outages, uncertainty,
and cancellation. PostgreSQL/HTTP/SDK acceptance is distinct from native KVM
park/restore, scheduler restart, and leakcheck; both are required for the complete
end-to-end goal. Rollback disables new admission while preserving reads, reports,
delivery, and recovery for already admitted operations.

Private code retention derives from an owned operation and its immutable
definition, including every verified member of the selected release graph.
Accepted/running work retains code and idempotency identity past timestamp
expiry; stopped work retains code through its result/recovery window. Cutover,
abort and rollback remove weighted traffic while preserving these references.
Private cleanup receipts use `customer_operation_code_pins`, separate from
public revision deadlines. Admission, claim renewal, progress, recovery and
settlement never extend the public revision-header window or native release-set
deadline. The canonical private-pin migration preserves existing public
timestamps and backfills private receipts only from owned operation references,
including their full release graph.

Pin cleanup locks apps before deployments and rechecks owned references in a
fresh READ COMMITTED statement after acquiring the app locks. Retained references
are excluded before paging, and a page considers at most
`api.RevisionPinCleanupPageMax` (500) deployment IDs, each with at most one
public and one private receipt. Both deadlines must expire, and each receipt
present in the cleanup snapshot must actually be deleted before code is retired.
A concurrent receipt renewal therefore preserves code. Expiry, owner deletion
and inconsistent admission metadata release the private reference without an
unlimited timestamp pin or a renewal heartbeat.

Fresh admission and safe retry lock every owned release app in ID order before
its deployments and execution rows, then revalidate all members and release
usability. A release selected by the definition receives the same checks as an
explicit request; locking only the originating app cannot protect another graph
member from code cleanup.

Customer HTTP operation claims use `X-Gregale-Customer-Operation-Id`. Managed
exclusive operations retain their own identity and result negotiation. A
customer claim cannot substitute a managed operation header for its attempt
proof. Ordinary HTTP definitions validate the complete handler response against
their output schema. A managed result envelope requires an explicit adapter;
an unexpected envelope preserves backend completion evidence and requests
reconciliation instead of silently decoding or executing the handler again.

## Bounded HTTP preview admission — 2026-10-05

The local HTTP continuation uses an operator-owned JSON policy selected by
`operations_preview_policy_path` in apid and gatewayd-internal TOML. Empty is
closed. Enabled policies bind exact canonical account/app IDs, an environment,
individual platform tenant IDs and a UTC window. Bounds are centralized at
64 KiB, ten cohorts, ten customers per cohort and one hour. Authentication,
ownership and plan quotas remain independent constraints. apid customer starts
also require configured workload trust and private result storage.

Admission reads the regular policy file on every decision; malformed, missing,
expired or group/world-writable policy fails closed. Atomic replacement permits
rollback without restarting daemons or dropping runtime trust. The durable
gateway resolver remains installed while closed so declared operation routes
cannot become ordinary handler requests. Protocol upgrades are rejected for
matched operation routes. Source admission is checked before manifest mutations.

Policy closure affects new admission and resubmission, including a duplicate
idempotency key. It leaves accepted work, retained reads, reporting, delivery,
cleanup and explicit recovery available. It is not a transaction barrier across
nodes: an earlier admitted decision can still commit. Fleet rollout verifies
each serving node and separately observes accepted executions. This mechanism
does not qualify native lifecycle or fleet availability; those passes remain
required before launch. See the [preview runbook](../ops/operations-http-preview.md).

## Customer history continuation — 2026-10-05

Customer discovery is a read projection above the existing operation ledger.
The tenant self-service route requires explicit app and environment selectors,
authenticated tenant ownership, bounded summaries and descending creation/ID
keyset pages. Cursors bind identity and filters without granting authority.
Input, results, storage locations, delivery errors and runtime capabilities stay
out of the history projection. Existing result retention applies: active work
remains visible, expired settled work does not. Pages are live views; progress
updates cannot reorder work, while filter membership and retention can change.
This read path stays available after admission rollback and uses the existing
tenant creation index; it introduces no new state table or execution adapter.

## Read-only submission diagnostics — 2026-10-05

The owning account can inspect an exact deployment and tenant through the
Operations doctor endpoint and CLI. Observations reuse the admission policy's
current-file decision, existing account-wide pending count and immutable contract
compiler. They reserve no slot, grant no admission and perform no external probe
or state mutation. Responses contain sanitized reason codes, deployment and
definition pins, an observation timestamp and responding-node scope. This is an
advisory read across changing metadata, not an admission transaction.

Submission blockers and unknown prerequisites determine the observed submission
state. Completion delivery warnings remain independent. Configuration presence
is distinguished from runtime usability; gateway, reporting, storage I/O, native
lifecycle and fleet rollback qualification remain explicitly unverified. These
reads stay available after admission closes and do not alter execution, recovery,
idempotency or delivery semantics. Bounds remain centralized in pkg/api/limits.go.

## Completion delivery evidence and decisions — 2026-10-05

The owning account can read an operation-scoped delivery snapshot and bounded,
newest-first attempt history with account read scope and MFA. Authorization
checks the immutable operation/definition destination, delivery account/app,
event and embedded operation identity. Attempts reuse the transport ledger;
raw errors, URLs, secrets and bodies are omitted. Receiver cooldown is a separate
ledger observation, not an external probe. Expired transport rows are reported as
`delivery_expired`; retained business success is preserved.

The additive `/delivery-retries` API accepts a stable retry ID, exact delivery ID
and explicit expected replay generation. The operation row serializes retry
decisions. A transaction commits the dead-to-pending reset, generation increment
and immutable receipt together, without invocation/execution/result writes or
subscription locks. A replay returns its original receipt before checking the
current transport state or plan. Changed payloads conflict and distinct IDs at
one observed generation have a single winner. At most 32 receipts are retained
per operation, with a composite primary key and cascading parent retention.
There is no delivery foreign key: confirmed decisions outlive transport cleanup.
Receipt timestamps use PostgreSQL microsecond precision in both stores to retain
exact replay equality. The legacy reset API retains its existing semantics.

The account CLI records API origin, current account, operation and retry request
in an immutable private fsynced file before mutation, then records a distinct
acknowledgement bound to the request digest. Lost replies replay that request;
identity/payload conflicts, unsafe files, invalid acknowledgements and expired
unconfirmed receipts fail closed. `queued` describes the decision at its recorded
time. Callers must inspect the delivery for live transport status. No decision
repeats business work or relaxes receiver cooldown/delivery policy.

## Cooperative HTTP control — 2026-10-05

Running ordinary HTTP handlers may observe cancellation intent, the admitted
deadline and live lease through a workload-authenticated control read. Account,
app, instance, invocation, attempt and capability must match the current claim;
an expired deadline is rejected even if an earlier lease extends beyond it.
Both stores observe invocation and operation together, using the existing
lifecycle lock ordering. This read creates no report, event or durable state,
grants no renewal and stays available after admission rollback. The projection
omits business data and execution secrets. Poll recommendations are bounded at
100–1000 ms in pkg/api/limits.go and adapt to short scheduler claims.

The opt-in Node request scope checks control before business code and before
returning its result, polls with fresh assertions, supplies cooperative I/O
cancellation and bounds CPU checkpoints. Server observation durations consume
request latency; monotonic elapsed time and forward wall-clock steps can shorten
but never extend the local budget. Request cleanup aborts polling and pending
control I/O. Reporting and prepared file writes obey the scope's stop state.

Control reads are advisory and cannot atomically fence external effects. Code
that ignores cancellation may continue. An observed stop neither undoes an
effect nor certifies a safe retry or cancelled business outcome. schedd still
settles dispatched work under its pinned recovery policy; uncertainty remains
reconciliation unless the existing contract declares safe repeat execution.
Native lifecycle and park/restore qualification remain separate requirements.

## Backend execution identity ledger — 2026-10-06

Each operation generation binds exactly one real backend execution: an HTTP
invocation, workflow run, or Job run. The ledger derives its execution ID and
kind from mutually exclusive backend foreign keys. The current projection must
match the binding's operation, generation, ID and kind. Events and report
receipts reference a binding owned by the same operation. Workflow/Job runs
retain permanent operation markers after projection GC; binding queries also
check their app/account ownership and workflows' platform tenant. Backend
adapters must lock their execution row before the common operation row. HTTP writers that predate the common
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

## Workflow definition snapshots — 2026-10-06

A definition selects exactly one target: an ordinary HTTP method/path or a
named workflow from its owner-scoped deployment. Workflow targets declare a
result step and aggregate progress stage. A private nullable snapshot column
retains the selected deployment DAG; HTTP definitions keep a null snapshot.
The ledger checks the target shape and matching snapshot name, and deployment
lookup checks both the owning app and account. Definition adapters must validate
the bounded DAG, waits, dependencies and exception routes, and include the
snapshot content in the immutable revision. Customers cannot supply a private
snapshot. This schema seam does not enable workflow execution: native admission
and dispatch remain unavailable until their complete recovery contract is
qualified.

## Controlled native workflow admission source — 2026-10-06

The trusted adapter must commit the native run, verified tenant, immutable input
and bounded DAG, initial steps, operation projection, backend binding, acceptance
event, scoped receipt and private code references in one transaction. It creates
no HTTP invocation. Native and operation admission share the app active-run quota
and advisory key; that lock precedes code app locks to avoid an app foreign-key
lock cycle. Owner and tenant binding remain mandatory.

The private SQL helpers exclude permanently marked runs from legacy claims and
recovery, including after operation projection GC, and retain bound native
history. Legacy recovery must preserve the canonical resumed, foreach and
outbound semantics: lock an unmarked run, mark uncertain unsafe outbound effects,
close their attempts, then reset eligible running steps in the same transaction.
The helper queries are unwired; the public PgStore runtime does not acquire these
isolation guards merely because they exist. Controlled adapter validation and
public claim, advancement, recovery and cancellation guards remain activation
gates. Native admission stays unavailable until those gates and native lifecycle
qualification pass. These source seams do not establish exactly-once execution.

## Native workflow coordinator custody ledger — 2026-10-06

A scheduler coordinator needs custody separate from guest reporting authority,
bound to the operation, real native run, generation and monotonic claim attempt.
Only its digest is stored. The ledger checks positive integer generations and
attempts, a SHA-256 digest, a finite lease and the owned execution association.
Custody is operational clone state and cascades with its execution history.

A qualified adapter must lock the native run before the operation and re-read
custody after the run lock; a stale joined candidate cannot authorize stealing a
renewed lease. It must enforce the existing two-minute execution lease and the
positive PostgreSQL integer claim bound in pkg/api/limits.go. These private SQL
queries do not themselves validate a presented capability or enforce monotonic
claim transitions. Such checks remain mandatory in the controlled adapter.

Parking requires no unresolved running step and preserves an earlier callback
wake. Idle expiry or a callback needs fresh custody without changing run identity
or erasing confirmed steps. An expired coordinator with unresolved running work
instead revokes custody and records requires_reconciliation in the common
operation transaction; it stops the native run without resetting step evidence.
Suspension does not prove that an external effect failed. Idle suspended work
parks until tenant activation; unresolved running work requires reconciliation.
These unwired ledger seams leave native admission, fenced step dispatch and
instance-bound guest authority unavailable pending their qualified adapter.

## Fenced native workflow step writes — 2026-10-06

The scheduler step mutation seam requires current custody and exact consecutive native attempts. Resolved input is frozen on the first dispatch and reused for an already persisted retry. Attempt history and compact step results commit together; identical terminal receipts preserve their first finished time, while conflicting receipts are rejected. A released or expired coordinator cannot change step history. Suspension denies fresh step starts while allowing a still-owned in-flight step to record its confirmed result. Resolved inputs and outputs obey the admitted operation value bound; step attempt counters use `OperationWorkflowStepAttemptsMax` (2,147,483,647), and retained error text uses `OperationWorkflowStepErrorMaxBytes` (4 KiB), both in `pkg/api/limits.go`. The coordinator remains responsible for native DAG readiness and recovery classification. This internal write seam does not itself dispatch handlers, grant guest authority, settle business outcomes or enable workflow admission.

## Native workflow guest binding ledger — 2026-10-06

Controlled workflow reporting uses a separate instance capability for each
native step attempt. The scheduler binds it once under current custody, after
the native attempt is running, to a running instance of the pinned app and
deployment. A consumed binding survives instance deletion with a null instance
reference; deletion cannot authorize another dispatch of an unresolved attempt.
Only its digest is durable. Raw guest and coordinator capabilities are omitted
from JSON, and neither capability substitutes for the other.

Guest authority names the real workflow run, native step and native attempt;
it does not fabricate an HTTP invocation. Its hard deadline derives from the
native attempt's start time and handler timeout. The centralized
`OperationWorkflowHandlerDefaultTimeout` preserves the native 30-second default
and per-call condition checker timeout. Every report also requires the current
unexpired coordinator custody, original operation generation, running native
attempt and actual bound running instance. Renewal cannot extend the guest's
hard deadline. Tenant suspension denies new bindings while still-owned work
may preserve reported progress and immutable files.

HTTP and workflow reporters share the same progress and verified artifact
storage contracts through backend-specific authority checks. Workflow receipt
IDs scope the caller's bounded report ID by native step and attempt, so distinct
steps or retries cannot collide under one coordinator claim. Events and blob
receipts retain the real backend identity. API artifact preflight checks this
authority before reading or copying source bytes; attachment checks it again.
The Node runtime keeps typed execution context isolated to the delivered
request and forwards workflow fields without an invocation header. These
reporting seams remain internal; dispatch, aggregate progress, business
settlement and explicit confirmed-step recovery still gate public admission.
