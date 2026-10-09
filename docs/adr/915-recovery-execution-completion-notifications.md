# ADR-915: Recovery execution completion notifications

Date: 2026-10-09
Status: Accepted

## Context

`event_recovery.completed` reports admission completion, while queued handlers
can remain running or fail. ADR-914 saves confirmed terminal execution results
with their recovery items. Operators need a distinct notification when those
results cover every queued item, without polling or treating unknown evidence
as success.

## Decision

Add the app webhook event `event_recovery.execution_finished` for newly created
execution-mode recovery jobs. Admission must already be completed or cancelled,
including automatic expiry, and every queued item must have a saved result for
its exact replay invocation, generation and creation time. Pending admission,
running/retrying execution, unavailable evidence and uncertain outcomes block
capture. Failed, dead-lettered, expired, cancelled and superseded executions are
confirmed terminal outcomes, not successful deliveries. Jobs with no queued
items are marked captured without an event or execution-finished timestamp.
Routing jobs and jobs existing when the migration runs remain outside this
notification contract. No historical notifications are backfilled.

The scheduler's existing fanout sweep calls a separate optional store interface
with its existing bounded recovery batch. A SQLC claim locks one due terminal
job using `FOR UPDATE SKIP LOCKED`, backed by a partial due-time index. Read at
most the existing 10,000-item job limit using the recovery observation projection.
An unresolved job is deferred for ten seconds, centralized in `pkg/api/limits.go`,
so it cannot starve younger jobs. Read endpoints remain read-only. No invocation
trigger locks a job or directly emits a notification; capture follows existing
job-first lock ownership and keeps handler completion independent of webhooks.

In one transaction, mark `execution_notification_captured`, stamp
`execution_finished_at`, freeze the summary and capture matching enabled
app-scoped webhook IDs into the existing outbox. Capture is durable even when
there are no receivers. The marker prevents recapture after relay deletes the
outbox row or later subscriptions are created. Memory storage mirrors the
transition under its mutex and clones exposed timestamp pointers.

The payload includes existing frozen admission metadata, a terminal execution
summary, `execution_finished_at` and `unresolved_count=0`. The summary has
`tracked_count=saved_results=queued_count`, with no nonterminal/unknown buckets.
`outcome=all_succeeded` means all **queued** executions succeeded; skipped and
cancelled admission items remain visible separately and are not claimed to have
executed. Any other terminal execution gives `finished_with_non_success`.
`completed_at` retains its admission meaning; `execution_finished_at` is the
scheduler's capture time, not the exact last handler completion time or a webhook
acknowledgement. No payload data, errors, operator reasons or credentials are
included. The timestamp also appears in recovery status/list API and CLI output.

Derive event ID as UUIDv5 in the URL namespace over
`gregale:event-recovery:<job-id>:execution_finished`. Existing outbox uniqueness
and relay source-event/receiver uniqueness protect retries. Each receiver keeps
its own delivery ID, signing, retries, dead letters and operator retry. Delivery
is at least once; deduplicate on the stable event ID. No ordering is promised
between admission and execution notifications or across receivers.

Append a migration adding operational capture/due-time fields and widening the
outbox event constraint. Existing jobs start captured; future jobs default to
uncaptured. Job pruning cascades results and removes capture state after the
existing thirty-day retention measured from admission completion/cancellation.
Evidence must settle and be observed before that pruning; unproven results do
not eventually turn into a completion event. Frozen outbox/delivery payloads
follow their own existing retention. Clone registry marks the new fields
operational. OpenAPI, Go DTOs and generated Node/Python SDKs expose the new event,
payload and timestamp.

## Consequences

Apply the migration before API/scheduler binaries. Existing wildcard app webhook
receivers receive this new event for eligible jobs; explicit filters can opt in
separately. Update clients that strictly validate webhook event enums before
upgrading the API. Existing webhook CLI commands suffice; no automatic receiver
creation or external messages are part of implementation.

Downgrade API/scheduler binaries before applying Down. Keep the widened outbox
vocabulary so committed notifications can still be relayed. Removing capture
columns loses deduplication state; a later re-upgrade treats existing jobs as
historical and does not regenerate completion notifications. Execution-result
retention and the existing admission lifecycle events are unchanged.
