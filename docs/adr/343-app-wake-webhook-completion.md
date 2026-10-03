# ADR-343 · Durable app wake webhook completion

- **Status:** accepted
- **Date:** 2026-09-28
- **Extends:** ADR-076, ADR-342, ADR-344

## Decision

The scheduler records a UUID wake transition atomically with the
`evicted_cold → active` app status change. `app.woken` is created only after a
matching instance reaches `running` while that transition remains current and
the app remains active. Transition completion and insertion into
`app_webhook_event_outbox` share a transaction, with the transition ID as the
stable event source key.

The wake path completes the transition after the first instance is ready.
Schedd's webhook dispatcher also scans a bounded number of pending wake
transitions and completes them from an already-running instance. This recovers
the gap where schedd stops after the instance becomes ready but before the wake
path records the event. Failed wakes roll the app back to `evicted_cold` and
supersede their transition. A park request supersedes a pending wake under the
app row lock, so a later park prevents a stale `app.woken` event.

At completion, the event snapshots enabled matching app subscriptions and
enters ADR-344's durable outbox. Schedd then relays it to one delivery per
snapshotted subscription. The completed transition remains stored after relay,
so retrying a wake cannot enqueue a duplicate.

## Why

The previous `webhook.Emit` ran only after the ready instance returned to the
EnsureWake caller. A schedd exit between readiness and that call lost the
event. Persisting the transition with the status change gives recovery a stable
record while retaining the rule that `app.woken` requires ready capacity.

## Boundaries

- A pending transition is not a customer event. It completes only when the app
  is still active and a matching instance is running.
- Failed and superseded transitions produce no `app.woken` event.
- HTTP delivery remains at least once; receivers dedupe by the stable delivery
  ID.

## Verification

- A pending transition without a running instance produces no event.
- The dispatcher recovers a ready transition and relays one delivery.
- Failed boot and park-wins paths produce no wake delivery.
- Repeating completion does not duplicate an event.
