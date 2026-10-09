# ADR-836: Recovery notification delivery reports

Date: 2026-10-09
Status: Accepted

## Context

Recovery admission and execution completion notifications are captured durably,
but capture does not prove receiver acknowledgement. The relay deletes the outbox
after fan-out, deliveries can be pruned or cascade away with their subscription,
and a capture with no matching receivers previously left no receiver snapshot.
A job-scoped report must distinguish these cases without repeating delivery.

## Decision

Add `GET /v1/event-recoveries/{jobID}/notifications`, Go/Node/Python clients, and
`gregale events recovery-notifications JOB_ID`. Require the existing apps-read/admin
scope and MFA. Validate job ownership before observing notification metadata;
wrong-account or pruned jobs return not found. Reject all query parameters.

Store `notification_receipts` JSON on the retained recovery job when an admission
or execution event is captured. Freeze its stable event ID, capture time and
selected webhook UUIDs, including an empty selection. Capture and any nonempty
outbox insert share the existing transaction and receiver selection. PostgreSQL's
capture guard preserves the first selection and prevents reenqueuing a captured
event after relay deletes the outbox. Memory capture mirrors this under its lock;
relay preserves an exact job/event/receiver-to-delivery association across manual
retries, without inspecting payloads. Do not store
payloads, URLs or credentials in the receipt. Extend the operational clone schema
registry with the column; job pruning naturally removes the receipt.

Admission and execution appear as separate report entries. Do not infer whether a
historical cancelled admission emitted cancelled versus expired without evidence.
Execution notifications remain inapplicable for routing, zero-admission terminal
jobs and historically ineligible jobs. Active execution jobs await capture even
before their first delivery is admitted.

Use preserved capture snapshots first; a retained, scoped outbox with the exact
stable event ID can supply a historical full selection. Retained deliveries alone
show observed receivers but never establish a complete selection. Never backfill
receipts or reconstruct selection from current subscriptions on a health read.
No preserved selection means unknown, including a potentially empty historical
selection. A selected receiver without a retained delivery is awaiting relay only
while its outbox survives; otherwise its evidence is unknown. This covers pruned
history and receivers removed before or after relay without asserting which
occurred. Deleted subscription IDs remain visible in the frozen selection.

PostgreSQL reads use a repeatable-read, read-only snapshot. Match deliveries by
stable source event UUID, event name, account and app, never by payload inspection.
Use existing source-event/receiver indexes and SQLC queries. Both stores bound the
read by the existing five-second request budget. Return at most 100 receivers per
notification (current plan quotas permit at most 25 subscriptions per app), with
bounded sorted observations and explicit incomplete counts if larger evidence
exists. Receiver counts overlap neither deliveries nor invocation executions.
`acknowledged` requires a known, complete selection with every selected receiver
currently succeeded. Unknown evidence prevents it; an empty known selection is
`no_receivers`, not acknowledgement. HTTP success proves transport acknowledgement,
not the receiver's downstream side effects. Manual retry changes the current
status and generation; attempt counts reset, while existing attempt history
remains the source for earlier attempts.

Expose delivery status, attempt/generation, HTTP code, scheduling/acknowledgement
times and subscription availability. Do not include errors, payloads, headers,
target URLs or signing secrets. Link retained deliveries to existing paginated
attempt history. Offer the existing independent retry path/CLI command only for
retained dead deliveries with a current scoped subscription. The report never
captures, relays, retries or alters any handler. Existing retry authorization and
write-scope/MFA requirements still apply.

## Rollout and downgrade

Apply migration `20261009140935781` before API and scheduler upgrades. No historical
snapshot is fabricated; old captures may remain unknown even with some retained
successful deliveries. Roll back binaries before Down. Dropping the receipt
column discards selection evidence; reapplying it starts empty and cannot recover
pruned selections. Existing notification markers, webhook deliveries, attempts
and independent retry behavior remain in place.
