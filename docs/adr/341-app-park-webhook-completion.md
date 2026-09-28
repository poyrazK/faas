# ADR-341 · Durable app park webhook completion

- **Status:** accepted
- **Date:** 2026-09-28
- **Extends:** ADR-076, ADR-340

## Decision

The API records a UUID park transition in the same transaction that changes an
app to `evicted_cold`. It emits `app.parked` only after the app remains parked
and the instance ledger has no live rows. The transition row and webhook event
are completed atomically; a completed transition stays recorded after the event
is relayed so an idempotent park retry cannot create a second event.

The API attempts completion after its existing drain wait. Schedd also scans a
bounded number of pending park transitions on each webhook dispatcher cycle.
That recovery path completes an event when the API exits after requesting a
park but before it can observe the drain. If a wake supersedes the park, the
pending transition is marked superseded and emits no parked event. A later park
gets a new transition ID.

At completion, the event snapshots the enabled matching app subscriptions and
enters `app_webhook_event_outbox`. The existing relay creates one delivery per
snapshotted subscription. Relay and API retries use the transition ID as the
source key.

## Why

The prior `webhook.Emit` ran after the status update and drain wait. A process
exit between those operations could lose the event, and repeating a successful
park could create another delivery. Recording the transition with the status
change gives recovery a durable marker while retaining the existing rule that
`app.parked` means the app has actually drained.

## Boundaries

- A pending transition is not itself a customer event. `app.parked` is created
  only after the instance ledger is drained and the transition remains current.
- App wake completion remains separate. `app.woken` must be recorded after an
  instance is ready, not when the app status first changes to active.
- Webhook delivery remains at least once over HTTP; receivers dedupe using the
  stable delivery ID.

## Verification

- An undrained app does not produce a park event.
- Dispatcher recovery completes a drained transition after the request path
  stops, then relays one delivery per matching subscription.
- Retrying a completed park does not duplicate its delivery; a later park after
  a wake receives a new transition ID and emits a new event.
