# ADR: Bounded Python preparation metrics

- Status: Accepted
- Date: 2026-10-17

## Context

Python emits fixed primitive preparation lifecycle events, but applications must
otherwise aggregate them themselves. Node already provides bounded counters and
duration histograms with explicit counting, clipping and observation limits.

## Decision

Add `EventAcknowledgmentPreparationMetrics` with synchronous `record` and detached
JSON-safe `snapshot`, plus public snapshot/histogram types and the immutable
duration-bound tuple. Match Node's fixed event/outcome/reason/stage vocabularies,
separate counter families, inclusive cumulative histogram bounds and sticky
safe-integer saturation, with Python field names. Initialize every fixed label
to zero and retain no source events, identities, tokens, payloads or raw errors.

Accept exact current SDK frozen/slotted variants and exact plain dictionaries.
Read SDK variants through captured native slots and reject other objects without
property access. Validate the full closed shape and native primitive values
before mutation. Raise a fixed `ValueError` outside validation exception handling,
discarding rejected data and validation causes before exposing the error.

Return fresh dictionaries/lists instead of frozen mapping proxies so snapshots
remain directly JSON serializable. Copies share no mutable collector state.
Require the host to serialize record/snapshot calls in one execution context;
same-thread asyncio calls naturally do so without awaits. Add no lock, thread
binding, exporter, timer, listener, transport or background task.

Show one long-lived collector attached to the existing version-5 job mode. Keep
snapshot export outside processing callbacks and preserve all existing processing,
ownership, cancellation, receipt and acknowledgment behavior.

## Consequences

No schema, route, migration, registry or service change is needed. Metrics remain
process-local observations without deduplication, active-work gauges, durable
checkpoints, reset, restore or merge. Duplicates count again; missing events and
crashes prevent completeness claims. Saturation indicates arithmetic clipping
only. Exporters own temporality, unit conversion, process identity and restart
detection. Retained receipts/intents remain recovery evidence. See the
[Python metrics contract](../event-acknowledgment-python-metrics.md).

## Validation

Use compilation/lint, Python 3.11 syntax, isolated generation, wheel/sdist inclusion,
schema/ADR/documentation checks and static vocabulary/bucket/source comparisons.
Add no tests, change no assertions and run no local tests. Hosted acceptance for
invalid inputs, cumulative histograms, clipping, snapshot isolation and lifecycle
counting remains pending before release.
