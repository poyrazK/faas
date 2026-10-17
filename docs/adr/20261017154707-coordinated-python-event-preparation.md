# ADR: Coordinated Python event preparation

- Status: Accepted
- Date: 2026-10-17

## Context

Python has local processing capabilities and durable acknowledgment intents, but
applications still maintain both lease lifetimes during preparation. Node already
provides a coordinated side-effect-free lifecycle.

## Decision

Add `run_prepared_durable` and frozen `EventAcknowledgmentPreparedOptions` to the
async Python worker. Reconcile/stage saved receipts first, skipping preparation,
local-table access and platform renewal when already committed. After a missing
receipt, start a bounded monotonic budget, confirm the exact platform attempt,
claim/refresh the local row and renew both independently.

Use a synchronous zero-argument factory returning fresh exclusive idle autocommit
async connection scopes. Release scopes between operations and keep no business
transaction open during preparation. Local renewal waits one third of its lease
after each completed scope; platform renewal keeps its independent HTTP monitor.
First failure signals cooperative cancellation. Stop waits, never active SQL,
and join local renewal before a final refresh and fenced transaction. Continue
the same platform monitor through completion.

Commit business writes, receipt, conditional claim removal and completion intent
together through existing hooks. Preserve the local linearization point and
receipt-first replay. Prepared data stays process-local and is never stored/logged.
Bound preparation to 1–2,147,483,647 ms and local lease to 3–2,147,483,647 ms with
default 30,000. Reject booleans/fractional values and snapshot effective options.

Preserve cooperative ownership, bounded storage diagnostics and unknown-COMMIT
precedence even when renewal discovers uncertainty during failure cleanup.
Confirmed intent commits remain confirmed after late cancellation, renewal or
scope cleanup failure. Attempt to close connections after failed scope cleanup;
factories own bounded cleanup/discard. Never infer rollback or automatically choose
a negative action. Select this lifecycle with explicit version-5 jobs without
relabeling older modes.

## Consequences

No schema, endpoint, migration or service changes are needed. Applications own
protected durable jobs, availability, bounded driver/factory calls, cooperative
side-effect-free preparation, short transactional handlers, callback recovery
and retention. Cancellation cannot forcibly stop physical work; timers cannot
run on a blocked/paused host. External effects need idempotency or an outbox.
Platform renewal is not a distributed SQL transaction. See the
[Python preparation protocol](../event-acknowledgment-python-preparation.md).

## Validation

Use compilation/lint, Python 3.11 syntax, isolated generation, wheel/sdist inclusion,
schema/ADR/documentation and static lifecycle checks. Add no tests, change no
assertions and run no local tests. Hosted concurrency, takeover, expiry, callback,
rollback, cancellation and uncertain-COMMIT qualification remains pending before
release.
