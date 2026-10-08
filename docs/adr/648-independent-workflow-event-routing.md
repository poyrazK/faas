# ADR-648: Independent workflow event routing

- **Status:** accepted
- **Date:** 2026-10-07
- **Amends:** ADR-432, ADR-606, ADR-647

## Context

Application event recipients have independent routing leases and replay
generations. Workflow admission authenticates the parent outbox lease, so a
captured workflow forces the entire receipt, including ordinary application
recipients, to retain whole-event ownership. This limits selective recovery
while another recipient remains in routing backoff.

## Decision

Schedulers adopt captured workflow-only and mixed workflow/application receipts
under the existing recipient ownership flag. Each workflow recipient gets its
own five-minute lease, backoff, cumulative attempt history and replay generation.
Its twelve-attempt routing budget renews on selective replay. Successful
application consumers and admitted workflows retain their original identities.

Recipient-owned workflow admission reads the captured envelope, definition and
filter from storage. It checks the recipient claim token, generation and lease,
then commits the workflow run, durable admission receipt, routing checkpoint,
history and aggregate routing settlement together. Admission reuses the app
quota lock shared by manual and scheduled workflow starts. It rechecks lease
validity after target and history writes; expiry rolls back every mutation.

The existing admission receipt remains authoritative after workflow run pruning.
Recovery reconciles a legacy committed handoff that had not recorded its routing
checkpoint, including when its run has already been pruned. A committed
checkpoint also protects success when the commit acknowledgement is lost.

When the workflow runtime is disabled, schedulers exclude workflow recipients
from due claims. Pending workflows consume no attempts and acquire no leases;
ordinary application recipients on the same receipt continue routing. Existing
abandoned claims wait for runtime activation before reclaim. Enabling the
runtime resumes workflow routing. Turning off new recipient adoption continues
draining already adopted workflows when the workflow runtime is enabled.

The public receipt and existing app-scoped routing replay endpoint expose
workflow routing recovery while siblings remain pending. `gregale events recover`
selects that action by the workflow's captured `subscription_id`. Once admission
is complete, the workflow run and step APIs own execution recovery. Routing
replay cannot create a second run for an admitted recipient.

## Compatibility and limits

This uses existing recipient and workflow receipt tables; no new migration or
public response fields are required. Set `FAAS_EVENT_RECIPIENT_CLAIMS_ENABLED=0`
on every scheduler during a mixed-version upgrade. Upgrade all schedulers to
support independent workflow admission before enabling adoption. Retained
adopted workflow receipts require compatible binaries even after adoption is
disabled; follow ADR-647's retention and downgrade constraints.

Explicitly disabled adoption preserves whole-event workflow routing for new
receipts. Stores without the atomic workflow recipient admission capability
also retain that path. Receipts without snapshots retain legacy routing.

Workflow definition snapshots, app quota, tenant binding checks, runtime
activation and retention remain unchanged. Workflow steps execute at least once
and require idempotent side effects. Publication and execution have no FIFO
guarantee. The consumer backlog projection continues to cover application
recipients. Historical backfill continues to skip receipts containing workflows;
workflow routing evidence is available through event receipts and routing history.

## Qualification

Memory and PostgreSQL acceptance cover concurrent admission, stale tokens and
generations, expired leases, captured filters, trigger replacement, retained and
pruned legacy handoff reconciliation, and tenant authorization rechecks.
PostgreSQL fault injection covers admission receipt and routing history failures,
lease expiry during target lock contention and after history writes, proving
that runs, admission receipts and routing checkpoints roll back together.

Scheduler acceptance covers workflow-only and mixed receipts under both ownership
settings. A mixed event exercises successful app/workflow recipients, a filtered
workflow, failed app/workflow recipients and a workflow sibling in backoff.
Selective recovery, restart with adoption disabled, duplicate publication and
a lost admission acknowledgement preserve successful consumers and one run per
workflow recipient. Disabled workflow runtime leaves workflow attempts at zero.

Public API acceptance verifies publish, receipt recovery actions, selective
workflow replay, run identity and routing history while a sibling remains
pending. CLI acceptance verifies workflow routing action selection and dry runs.
These qualify routing and recovery; native guest execution, fleet staging and
repository-wide CI remain release gates.
