# ADR: Python sustained acknowledgment recovery alerts

- Status: Accepted
- Date: 2026-10-17

## Context

Python can summarize retained callback recovery health, but applications still
need their own sustained alert loop. Go/Node distinguish confirmed breaches and
resolutions from lost evidence. Python needs the same semantics while keeping
active async database work and notification cleanup owned during cancellation.

## Decision

Add `awatch_event_acknowledgment_intent_health` with frozen options and metadata
alerts. Copy policy/owner scope before awaiting. Enable due/stalled thresholds
explicitly, reuse the existing health SELECT and apply the Go/Node confirmation
rules. Firing/resolution require two fresh complete strictly advancing database
observations spanning the sustain interval. Track database/wall instants and
monotonic durations with integer nanoseconds. Retain a fresh observation high-water
mark even for incomplete samples. Missing evidence resets proof and emits unknown
or confirmation-lost transitions, never resolution; suppress repeated unchanged
unknown reasons.

Use a synchronous factory returning a fresh exclusive async connection scope per
poll. This permits replacement after the health helper closes a failed connection.
Include acquisition, SELECT and cleanup in the cooperative read budget. Set a
stop event at the deadline and check elapsed time after cleanup; never abandon or
force-cancel active SQL, acquisition or scope exit. Await one read and its local
notifications before the next poll.

Reuse owned cooperative cancellation for the whole watch. Await synchronous/async
notification results sequentially. A notification exception stops the watch with
a fixed callback error and the frozen affected alert, without arbitrary causes.
Host cancellation takes precedence after callback cleanup and returns the native
cancellation category without resolving alerts. The application supplies bounded
driver/acquisition/cleanup/callback work and drains the watch before shutdown.

Preserve both modules and root exports through generation and packaging. Add an
explicit monitoring example. No schema, platform endpoint, registry or recovery
callback behavior changes.

## Consequences

Applications can monitor due/stalled local callback backlog with shared semantics
across SDKs. Coverage remains bounded to retained unfinished intents. An alert is
metadata, not proof of platform completion or business side effects. Alert state
is in memory and resets on restart. Notification persistence, incident identity,
deduplication, transport and available monitoring hosts remain application-owned.
No automatic startup, durable alert outbox or checkpoint protocol is added.

## Validation

Use compilation/lint, Python 3.11 syntax, static protocol review, isolated generation
without test files, wheel/sdist inclusion, schema and documentation/static checks.
No tests/assertions are added or changed and no local tests run. Hosted timing,
connection replacement, cancellation, evidence loss and notification qualification
remains pending before release. See the
[Python watch contract](../event-acknowledgment-intent-health.md#python-watch).
