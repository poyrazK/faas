# ADR-1011: Durable recovery review-ready notifications

Status: Accepted

## Context

Preview policies prepare immutable response plans (ADR-1009) and operators record
review decisions separately from application (ADR-1010). Plans otherwise require
manual polling before operators know they need attention.

## Decision

Schedd captures `automation.recovery_review_ready` once per prepared review. A
separate notification row shares review retention, is seeded atomically with new
plans, and is backfilled for existing retained plans by the migration. Capture
locks one due row with SKIP LOCKED and commits a stable event ID, frozen enabled
app-scoped webhook recipients, and the application outbox insertion together.
MemStore stages the same evidence under its mutex. Polls use the existing bounded
recovery policy batch and one-minute deferral interval.

Before capture and each HTTP attempt, inspect review disposition and exact target
freshness. Any final decision, application (including partial application), stale
plan, or pruned review suppresses transmission. Unknown evidence or check failures
defer capture or leave the delivery lease for retry. A send guard verifies account,
app, capture event identity, frozen recipient membership, and full frozen payload.
The check is advisory: a decision or generation change concurrent with an already
started transmission cannot recall that request.

The payload contains app, parent recovery job, review ID, policy version, prepared
targets with run IDs, generations, triggers and actions, review/status API links,
and CLI commands for inspecting and exporting the plan. No action runs implicitly.

Reuse the existing webhook relay, signing, retry ladder, receiver cooldowns, claim
fences, delivery ledger and attempts history. Retries reuse the event and delivery
identities. This is at-least-once HTTP delivery; receivers must deduplicate by event
ID when acknowledgement is lost. There is one logical event and one delivery per
captured receiver, not a guarantee of one external HTTP request.

## Consequences

Review GET/list and the CLI show durable capture evidence (`queued`, `no_receivers`
or `suppressed`) and frozen recipient IDs. Capture is separate from delivery;
queued does not mean acknowledged. No matching receiver is a final captured
outcome; adding a receiver later does not replay historical reviews. Existing
undecided retained plans are eligible on upgrade, with receivers frozen at capture.

Suppressed deliveries use the existing `dead` state with an explicit suppression
reason and no HTTP request. Manual delivery retries pass through the same guard.
Unknown checks preserve pending work without consuming a network attempt. Delivery
history uses existing webhook retention; capture evidence follows parent-job review
retention. Downgrade refuses retained capture or delivery evidence.
