# ADR-1012: Recovery review execution outcomes and completion notification

Status: Accepted

## Context

A review can be applied while its retry child is still waiting for admission or
execution. Operators need a final result for every action and a single plan-level
completion event, including plans that mix retries and cancellations.

## Decision

Review status keeps application freshness (`status`) separate from execution
`outcome` and `complete`. Each action reports the exact execution generation,
workflow and child job links, and retained evidence source and timestamp.
Cancellation completes from its matching durable keyed cancellation receipt.
Retries complete from saved terminal workflow results for their exact admitted
generation. A finalized skipped admission is a failed action; a finalized
cancelled admission is a cancelled action. Neither claims the workflow executed.
Live terminal observations, missing evidence and superseded generations remain
unresolved. The saved response CLI keeps its conservative execution-only
reconciliation; review outcomes additionally report terminal admission refusals.

Plan completion requires every action to be final. Failure takes precedence;
otherwise all cancellations yield `cancelled`, mixed success and cancellation
yields `completed`, and all successes yield `succeeded`. Incomplete plans report
`unknown`, `running` or `pending` based on retained action evidence.

The scheduler polls prepared reviews in the bounded recovery policy batch and
uses one-minute deferral for unresolved results. PostgreSQL locks one due row
with SKIP LOCKED and atomically saves the complete result, stable event identity,
frozen enabled app receiver list and `automation.recovery_review_finished`
outbox event. MemStore uses its mutex and a second capture check. A capture with
no receivers still retains its result and does not replay to later subscriptions.
Existing review notification rows are backfilled with a completion poll time.

The final snapshot is authoritative for later status reads, even after a run is
resumed again or live execution rows are pruned. Completion is a historical fact:
later approval, dismissal and generation changes do not suppress delivery. Each
HTTP attempt checks retained review scope, exact frozen payload and recipient
membership before unsealing a secret. Unknown guard failures preserve the claim
lease; mismatches and pruned reviews terminate delivery. Generic disabled-hook
checks still apply. Review and snapshot retention follows the parent recovery
job; webhook ledger retention is independent.

Use existing webhook signing, relay, retries, cooldowns and ledger. HTTP delivery
remains at least once: receivers must deduplicate by `event_id`. Queued capture
does not mean the receiver acknowledged delivery. No actions are applied by
capture, review decisions or status reads. Downgrade refuses retained completion
evidence and finished-event ledger rows.
