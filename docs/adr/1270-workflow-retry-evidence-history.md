# ADR-1270: Retry evidence repairs and alert transitions in history

## Status

Accepted, 2026-10-13.

## Context

Parent and child evidence repairs have immutable review receipts. Retry evidence
alerts retain their latest notification receipt. The shared automation history
needs these associations and must preserve earlier blocked/cleared transitions.

## Decision

Append `retry_parent_evidence_repaired` and `retry_child_evidence_repaired` to the
existing durable history ledger atomically with proof and repair receipts. Append
`retry_evidence_blocked` and `retry_evidence_cleared` atomically with notification
capture, including captures with no receivers. Identical repair replay and
unchanged evidence observations append nothing. PostgreSQL transaction rollback
also rolls back history; memory performs capture under its store lock.

History entries include optional evidence kind, exact resume count, repair request
UUID or notification event UUID, detailed diagnosis reason, and review/evidence/
job links. Repairs preserve trusted actor, operator reason and actual verified
outcome. Derive workflow, run and generation from the frozen retry plan; alert
entries preserve the observed evidence target and policy version. Never include
workflow inputs, outputs or secrets.

Old parent and child repair receipts appear as retained observations only when
no matching durable repair event exists. Read-only projection uses frozen review
plans while the source review remains retained. Job pruning cascades to reviews;
prospective durable events survive this, but old receipts cannot be reconstructed.
It creates no
new event or receipt and does not label old receipts durable. Latest notification
observations remain distinct from immutable transition events; historical
transitions are recorded prospectively and cannot be reconstructed from a latest
receipt alone.

Use existing history account/app authorization, workflow/run/version/time
filters, sequence-fenced cursor pagination, 30-day retention and per-app event cap.
An append-only migration expands the existing event-kind constraint and validates
evidence associations; existing candidate-evidence constraints remain intact.
Downgrade refuses to discard retained evidence events. No endpoint, new limit or
apid size change is needed. Existing history
CLI and SDKs expose the new optional fields; the CLI prints evidence provenance,
request/event associations, actor and diagnostic links.

## Consequences

A timeline can connect missing proof, an audited repair and subsequent clearance
without losing a block when the latest receipt changes. Clearance is evidence
attention resolution, not successful workflow execution. History recording does
not submit jobs, release slots or modify budgets or policy authority. Older
receipts remain explicitly retained observations, while prospectively captured
events remain immutable within existing retention bounds.
