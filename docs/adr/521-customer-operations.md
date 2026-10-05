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

All bounds live in pkg/api/limits.go. Qualification covers concurrent admission,
input conflicts, cross-tenant denial, pinned revisions, stale reports, durable
stream replay, retention, artifact authorization, delivery outages, uncertainty,
and cancellation. PostgreSQL/HTTP/SDK acceptance is distinct from native KVM
park/restore, scheduler restart, and leakcheck; both are required for the complete
end-to-end goal. Rollback disables new admission while preserving reads, reports,
delivery, and recovery for already admitted operations.

Private code retention uses owned operation references and separate code-pin
receipts. Admission and safe recovery lock the bounded release graph before
execution rows. Active operations retain their code and idempotency identity
through long waits; settled work reserves the full documented replay window.
Code cleanup and rollout changes preserve those references without extending
the configured public revision or release-header access window.

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
