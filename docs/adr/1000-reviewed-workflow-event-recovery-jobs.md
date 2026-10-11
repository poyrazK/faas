# ADR-1000: Reviewed workflow event recovery jobs

Status: Accepted

## Context

Automation event plans retain explicit acceptance recipient identities and reviewed
published versions, replay counts and routing generations. Local execution stops
when its CLI process disconnects. The existing event recovery engine already
provides durable items, account job quotas, rate permits, receipt protection,
pause/resume/cancel, history and notifications, but ordinary routing selection
intentionally excludes workflows.

## Decision

Add an optional `workflow_routing` plan to the existing routing job request. It
requires an account-scoped request UUID and at most 25 distinct event IDs. Parent
recovery and backlog filters cannot be combined with this explicit selection.
Select only original acceptance snapshots for the requested app and workflow.
Creation requires every selected recipient to remain a retained routing failure;
it does not replace reviewed expectations with current evidence. The worker uses
the same transactional publication, receipt, generation and replay-count fences
as guarded single-recipient recovery. Stale items are skipped with `changed`,
without admitting another replay cycle. Age deadlines remain authoritative.

Reuse existing tables, scheduler processing and job controls. Selection JSON
retains the reviewed expectations; no new queue is required. An append-only migration widens the existing request
identity constraint to allow reviewed workflow routing jobs while retaining its
account-scoped unique index. Downgrade refuses while such jobs are retained,
rather than deleting their idempotency identities. A
repeated request UUID returns the retained job before selecting candidates or
checking quotas, including after completion. Different normalized selection or
rate conflicts; operator reason is audit-only. Pause and cancel affect future
routing admissions, not already admitted workflow runs. Receipt protection and
job expiry retain their existing bounds. Routing jobs do not claim workflow
execution success, and read-only preflight reports fence eligibility as unknown.

The CLI submits existing reviewed event plans through `automations
event-recovery-start`; generic `events recovery-*` commands inspect and control
jobs. Workflow run resume plans and historical backfill recovery are outside this
extension and retain their existing controls.

## Consequences

CLI disconnection no longer interrupts submitted routing recovery. Per-item
outcomes and controls are available from another machine. Durable idempotency
lasts for existing job retention; submission after retention is a new job and
still cannot bypass original expectation fences. Ordinary event routing and
execution selections retain their current behavior.
