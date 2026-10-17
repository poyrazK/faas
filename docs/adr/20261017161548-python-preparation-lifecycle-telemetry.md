# ADR: Python preparation lifecycle telemetry

- Status: Accepted
- Date: 2026-10-17

## Context

Python coordinates preparation leases and exposes retained recovery inspection,
but provides no live phase/duration observations. Node already defines bounded
metadata events that keep diagnostic observations separate from recovery evidence.

## Decision

Add optional keyword-only `on_event` to frozen prepared options and snapshot the
observer before I/O. Emit frozen, slotted primitive dataclass variants using Node's
eight event kinds, seven stages, four outcomes and fifteen fixed reason literals.
Expose public event/stage/outcome/reason aliases. Include bounded monotonic whole
milliseconds, never identities, payloads, prepared data, tokens, results, raw
errors, arbitrary codes or transport diagnostics.

Observe owned operation start, new claim acquisition, preparation settlement,
one failure per lease, confirmed intent commit, one uncertain COMMIT and final
shutdown after coordinator cleanup joins. Emit no successful heartbeats and
bound volume to nine events. Background local renewal uncertainty retains its
stage and remains reportable after cancellation. Preserve existing confirmed and
unknown-commit precedence; diagnostics never choose recovery or alter cancellation.

Use a synchronous passive observer with exception isolation. Emit failure metadata
outside active processing exception handlers, preventing implicit exception-context
exposure if the observer itself throws. Start no observer tasks and await no
observer work. Close unstarted returned native coroutines;
consume existing Future/Task errors without retaining causes, cancelling or
joining borrowed work. Ignore other awaitables. Blocking/arbitrary side effects
remain application responsibility. Demonstrate a bounded local queue using the
existing version-5 job mode, without a new durable phase or record format.

## Consequences

No schema, route, migration, registry, transport or service changes are needed.
Events are best effort, without durable audit, sequence, replay or deduplication.
Applications own bounded diagnostic buffers, draining, availability and passive
callbacks. Observer time may consume budgets or stall timers. Retained receipt/
intent protocols remain recovery evidence; inspection grants no ownership. See
the [Python telemetry contract](../event-acknowledgment-python-telemetry.md).

## Validation

Use compilation/lint, Python 3.11 syntax, isolated generation, wheel/sdist inclusion,
schema/ADR/documentation and static event vocabulary/payload checks. Add no tests,
change no assertions and run no local tests. Hosted observer failure/awaitable
handling, ordering, secret exclusion, cancellation, lease loss and commit-boundary
qualification remains pending before release.
