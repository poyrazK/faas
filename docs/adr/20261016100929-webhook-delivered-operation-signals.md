# ADR-20261016100929 · Webhook-delivered Operation signals

- **Status:** accepted for implementation
- **Date:** 2026-10-15
- **Owner:** Operations maintainers
- **Related:** [ADR-1193](1193-webhook-submitted-customer-operations.md),
  [ADR-1197](1197-operation-event-waits-and-signals.md)

## Context

A provider webhook can start an Operation (ADR-1193), and an Operation can
wait for a named event that a signal delivers by business subject (ADR-1197).
The common case joins the two: an order Operation waits for
`payment.succeeded`, and the payment provider's webhook is the thing that says
so. Today the customer must run a function that receives the provider event,
reads the order ID from it, and calls the subject signal route with an API
token. That function holds a credential, can drop the event, and adds a hop
whose failures Gregale cannot see.

## Decision

**Binding mode.** An operation binding gains `mode`: `start` (the default and
the ADR-1193 behaviour) or `signal`. A signal binding names the same operation,
platform tenant and scope, plus:

- `subject: {type, id_from}`. The type must equal the subject type the
  operation definition declares. `id_from` is a JSON Pointer into the provider
  event payload, with the same rules as a definition's pointer into input.
- `signals: [{provider_event, event}]`, 1–32 entries with distinct provider
  event types. Each maps a verified provider event type to the operation
  event a `wait_for_event` step names.

`event_types` must be empty: `signals` is the filter. The binding PUT also
checks that the operation is a workflow and that its live workflow waits for
every mapped event, so a typo fails when it is saved, not when it is first
used. The binding body cap rises from 4 KiB to 16 KiB so the largest valid
signal binding fits.

**Delivery.** A verified event on a signal-bound endpoint starts nothing.

- An unmapped type, or a payload without a nonempty string at `id_from`, is
  `202 ignored`.
- Otherwise the event goes to the bound tenant's active Operations of the named
  definition about that subject, through the ADR-1197 signal path, at most 16
  per event. More is a limit error with `limit` and `observed`.
- The provider body is the signal payload, so the wait step's output is the
  provider event. It must be at most 64 KiB; a larger body is `413`.
- The signal ID is derived from the endpoint and the provider event ID. A
  provider retry is the same signal and lands once in each Operation.
- Operations that settle meanwhile, and Operations whose pinned workflow
  snapshot does not wait for the event, are skipped. A retry could fix neither.
- The answer is `202 signaled` when at least one Operation received the
  signal, with `duplicate` true when every receipt was a repeat. It is
  `202 unmatched` when none did. Any other refusal is non-2xx, so the provider
  keeps the event and retries.

**Tenant boundary.** Subject matching is scoped to the binding's platform
tenant. Another customer's Operation about the same subject ID is never
reached. The subject query gains an optional tenant filter; the account-level
subject signal route (ADR-1197) passes none and is unchanged.

**No receipt rows.** Start mode keeps a durable receipt because admission must
happen exactly once. Signals already have per-Operation idempotency in the
workflow event and the `signal_received` event, so a signal-mode delivery
writes no receipt of its own. Each Operation's history and timeline record the
signal, with the derived `signal_id`.

## Consequences

- Provider events resume waiting Operations with no customer code and no API
  token in the path.
- An event that arrives before any Operation exists for its subject is
  `unmatched` and dropped, not held. Within a single Operation, a signal that
  arrives before its wait is still held by the run (ADR-1197).
- Switching an endpoint between modes is a binding update under
  `expected_version`. Events already accepted in start mode are still answered
  from their receipts.

## Rejected alternatives

- **Holding unmatched events until an Operation appears.** That is an
  unbounded inbox keyed by customer data. Providers already retry, and an
  Operation that starts later can be told by the API.
- **Taking the subject from the operation input pointer.** Provider payloads
  have their own shape; one definition can be started by an API call and
  signalled by a provider.
- **A separate signal binding beside the start binding.** One endpoint has one
  routing owner (ADR-1193's delivery takeover). Two owners would need a
  precedence rule for every event.
