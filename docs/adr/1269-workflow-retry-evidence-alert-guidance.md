# ADR-1269: Retry evidence alert guidance and transitions

## Status

Accepted, 2026-10-13.

## Context

Retry evidence can be repaired before admission (parent) or after submission
(child). Existing evidence alerts capture once per action and identify only
`evidence_unknown`, leaving operators to find the relevant generation themselves.

## Decision

Use the same verified diagnosis as the repair endpoints inside the notification
worker. Pending actions inspect their exact parent item; applied actions inspect
their exact child. Include evidence kind, run, resume count, diagnosis reason,
repairability, diagnosis URL, job URL and the matching CLI command. Repairable
terminal proof reports `terminal_outcome_not_saved`; unsafe or unavailable proof
reports `evidence_unknown` with its detailed diagnosis reason. Never expose input
or embed a runnable repair with an invented request ID or reason.

Retain the latest notification receipt with state, sequence, evidence kind and
diagnosis reason in the existing JSON receipt column. Capture changed diagnoses
and proven clearance with distinct stable event identities. The first event keeps
the existing identity; legacy receipts count as the first capture. Unchanged
observations and repeated clearance stay quiet. Existing historical payloads and
recipient snapshots remain immutable. A capture without receivers does not replay
when a receiver is added later.

Pending parents also require the exact live generation to remain retained; a
saved parent result cannot clear a missing or superseded live run.
Clear only on exact saved terminal proof, a confirmed result, or verified child
execution progress. Missing, superseded, conflicting or unverifiable completion
proof cannot clear a blocker. Running parent evidence cannot authorize the old
saved-failure plan. Dismissed or suppressed plans do not emit recovery merely
because they stopped. An active evidence alert remains observable while a policy
is paused or disabled, so actual evidence restoration can clear it; policy changes
alone cannot. New alerts retain existing policy eligibility checks.

Serialize inspection and receipt/outbox capture under the memory store lock or
the PostgreSQL policy/action transaction. SQLC polling observes active receipts
and existing candidate actions; no schema migration is needed. Notification
capture never mutates worker evaluations, submits children, charges/refunds a
budget, releases a slot or changes policy authority.

## Consequences

Operators receive the exact diagnostic route and repair guidance from the alert.
`state: recovered` means evidence attention cleared, not workflow success.
Receipt fields and webhook fields are optional for old consumers. SDKs follow the
canonical OpenAPI contract. Historical webhook delivery may follow recovery;
consumers deduplicate by event ID and order observations by their capture time.
