# ADR: Node sustained acknowledgment recovery alerts

- Status: Accepted
- Date: 2026-10-17

## Context

Node exposes bounded recovery health, but operators need notifications when due
callback backlog or stalled recovery persists. The existing Go watch defines
freshness, incomplete evidence and sustained firing/recovery semantics. The Node
pool interface cancels database calls cooperatively and cannot interrupt arbitrary
driver acquisition or SQL by itself.

## Decision

Add an explicit in-process Node watch with the Go thresholds, default budgets,
evidence-loss reasons and sustained confirmation behavior. Snapshot options and
scope before yielding. Poll sequentially and use the existing metadata-only
health read. Require two distinct, complete, fresh, advancing database observations
spanning the sustain interval for firing and resolution. Measure sustain and poll
continuity with a monotonic local clock, and freshness against the local wall clock.
Missing proof becomes unknown or confirmation lost, never resolved. Preserve the
fresh observation high-water mark and suppress repeated unchanged unknown reasons.

Apply a cooperative per-read deadline including pool acquisition, rejecting late
results even if the timer callback was delayed. Await actual driver completion;
never race SQL, release a busy connection or start overlapping polls. Require
application-configured driver/statement timeouts to bound unresponsive operations.

Deliver immutable metadata alerts to sequential, awaited local callbacks. A failed
callback stops the watch with its metadata alert and a sanitized error, without
retaining the raw cause or retrying an uncertain notification. Host cancellation
waits for active work and rejects with its abort reason, emitting no resolution.
Keep process lifetime, callback persistence, transport and downstream incident
identity application-owned. State is in memory and resets on restart, as in Go.

## Consequences

No schema, route, recovery-claim or acknowledgment callback changes are needed.
Incomplete coverage and missing/frozen observations cannot clear a firing condition.
After confirmation is lost, fresh below-threshold evidence does not resolve a
previous incident; applications own that reconciliation policy. A parked VM is not
kept alive, and there is no durable alert outbox or exactly-once notification claim.

See the [Node watch guide](../event-acknowledgment-intent-health.md#node-watch).

## Validation

Use Node generation/build, packaged export/declaration inclusion and static source
and documentation checks. No new tests, assertion edits or local test execution
are part of this increment. Hosted acceptance remains necessary for database
cancellation, timing, concurrency and callback behavior before release.
