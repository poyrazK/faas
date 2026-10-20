# ADR-1197 · Operation event waits and signals

- **Status:** accepted for implementation
- **Date:** 2026-10-15
- **Owner:** Operations maintainers
- **Related:** [ADR-1194](1194-operation-waits-and-approvals.md),
  [ADR-1196](1196-operation-deadlines-and-budgets.md), ADR-1193 (webhook
  submission)

## Context

An operation workflow can wait for a timer or for an approval (ADR-1194). Much
business work instead waits for something outside: a payment provider
confirming a charge, a carrier picking up a parcel, a partner finishing a check.
The native workflow engine already parks a step on a named event, holds events
that arrive early, and wakes the run when one arrives. Operations rejected those
steps because nothing tied the event to the Operation: the native event route
takes a workflow run ID the caller never sees, and the Operation's history would
not record the event.

## Decision

**Event waits.** An operation workflow may include `wait_for_event: <name>`
steps. Each needs a `timeout`, so the wait always ends. The name is a bounded
lowercase name (`[a-z][a-z0-9_.-]*`, at most 63 bytes) outside the reserved
callback namespace. A step may not be both an event wait and an approval. While
parked, the Operation is `waiting` with `waiting.kind: event` and
`waiting.event`. A timeout is certain, since waiting performs no external
effect, and settles `failed` with `failure_code: event_timeout`. The deadline
(ADR-1196) also ends an event wait.

**Signals.** An account delivers an event with
`POST /v1/apps/{slug}/operations/{id}/signals`
`{signal_id, event, payload}`. It requires deploy-write scope and MFA, like
cancel and recover.

- `signal_id` makes delivery idempotent per Operation. It maps to a
  deterministic workflow event ID. A repeat with the same event and payload
  returns the original receipt, even after the Operation settles. A different
  event or payload under the same ID is `409 operation_signal_conflict`.
- The event must be one the Operation's workflow snapshot waits for, or the
  route answers `422 operation_signal_unexpected`, so a typo cannot sit unused.
- A settled Operation takes no new signals (`409 operation_not_active`).
- A signal that arrives before its wait is held by the run and consumed when
  the wait starts. The receipt's `waiting` flag says which case happened.
- The signal, the workflow event and a `signal_received` Operation event commit
  in one transaction, with the run locked before the Operation (the
  cancellation lock order). The payload is the wait step's output, and later
  steps read it through workflow templates. Payloads are canonical JSON, at
  most 64 KiB.

**Signals by subject.** `POST /v1/apps/{slug}/operation-signals`
`{scope, operation, subject: {type, id}, signal_id, event, payload}` delivers to
every active Operation of that name about that business subject. Callers such
as a payment webhook handler know the order, not the Operation ID. It reaches at
most 16 Operations, oldest first; more is a limit error with `limit` and
`observed`. Operations that settle while the signal is delivered are skipped,
and the response lists one receipt per Operation reached. Matching uses the
existing subject history index.

**One path in.** The native `POST /v1/workflows/runs/{id}/events` route, and its
tenant variant, refuse runs that belong to an Operation with
`409 operation_signal_required`. Every event an Operation sees then appears in
its history and timeline, as ADR-1194 already does for approval callbacks.

## Consequences

- Operations can model "wait until something else happens" without polling,
  and external systems can resume them by business key.
- No new tables: signals are workflow events with deterministic IDs, plus one
  new Operation event type, `signal_received`.
- Delivery is at least once from the caller's side and exactly once into the
  run, keyed by `signal_id`. Gregale still promises nothing about effects the
  next step performs.

## Rejected alternatives

- **Correlating by a free-form payload field.** The Operation subject is
  already the declared, indexed business key, and it is fixed at acceptance.
- **Signalling through the native run route.** It hides the run ID boundary
  Operations keep, and bypasses the Operation's history.
- **Routing provider webhooks straight to waiting Operations now.** That needs
  an endpoint binding mode on top of ADR-1193;
  [ADR-20261016100929](20261016100929-webhook-delivered-operation-signals.md) adds it.
