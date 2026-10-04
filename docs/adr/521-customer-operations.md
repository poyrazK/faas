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
