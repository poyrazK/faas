# ADR 1033: Consumer-group dead-letter webhooks

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Emit `realtime.consumer_group.dead_lettered` when a consumer-group snapshot
transitions from a non-dead status to `dead`. A PostgreSQL update trigger enqueues
the event in the existing app webhook outbox in the same transaction. A rollback
cannot leave an alert for a transition that did not commit. Explicitly select
this event on an enabled app-scoped webhook; wildcard subscriptions and account
release webhooks do not receive it. Recipients are captured at the transition.
There is no backfill for existing dead letters.

Include app/endpoint/group UUIDs, group name, channel, decimal-string sequence,
attempts, maximum attempts, bounded failure code and occurrence time. Exclude
message payloads, claim tokens, credentials and raw exception messages. Use the
existing signed webhook dispatcher, retry policy and delivery ledger; consumers
must deduplicate delivery IDs because delivery is at least once. The outbox's
UUID identifies this transition rather than the source message.

Explicit retry exhaustion and lease expiry use the same transition. Lease expiry
continues to be recognized on a later claim, not by a timer; without another claim
its alert is delayed. Dead-letter inspection can show an exhausted expired lease
before it has transitioned and therefore before a webhook exists. Replaying a
message returns it to ready and resets its attempt budget. If it later fails
again, emit a new event; editing an already-dead row does not emit another one.
Enqueue failures roll back the state transition so a claim/settlement can retry.

Keep failure reasons as existing printable ASCII `failure_code` values (up to
64 bytes). Add a Node worker `failureCode(error, message)` callback, shared by
account and scoped-credential helpers, defaulting to `handler_failed`. Never copy
exception text automatically. An invalid or throwing callback terminates the
helper without settlement, leaving its claim to expire. The server already
validates direct Node/Python settlement requests.

Link group names to their inspector dead-letter lists and individual dead letters
to retained-history inspection. Snapshot payloads remain viewable if source
history has expired. Replay stays an explicit authorized API operation after the
worker is repaired; no automatic replay loop or unauthenticated webhook action.

## Deployment and validation

Apply migration `20261012102700479_realtime_consumer_group_dead_letter_webhooks.sql`
after consumer-group and webhook-outbox migrations, before selecting the new
webhook event. Downgrade removes the trigger but retains vocabulary for queued
webhook deliveries. Go/Node builds and static specification checks are the
validation scope; runtime, migration and UI checks remain deferred by request.
