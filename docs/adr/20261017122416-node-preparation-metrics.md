# ADR: Bounded Node preparation metrics

- Status: Accepted
- Date: 2026-10-17

## Context

Prepared Node workers emit bounded lifecycle metadata. Applications still need
to implement counters and timing aggregation before exposing those observations
to existing metrics systems. Aggregation must preserve local commit uncertainty
and receipt replay semantics without retaining event identities or payloads.

## Decision

Add `EventAcknowledgmentPreparationMetrics` with a receiver-independent `record`
observer and a pure detached snapshot. Use fixed event/outcome/reason/stage maps
and fixed cumulative duration buckets. Count operation outcomes only from
shutdown, receipt creation/replay from intent commit, renewal failure by lease
and reason, and unknown commit observations by stage. Collect callback and
whole-operation duration samples from their respective settlement events.

Validate the complete current event contract before mutation. Copy own data
properties and reject unknown fields, accessors, invalid labels or nonsafe
durations without retaining raw objects or errors. No dynamic labels or
per-operation state are added. Counter label maps satisfy the typed vocabulary
exhaustively at build time.

Use finite nonnegative safe integer arithmetic. Clamp overflows and expose sticky
saturation flags rather than wrapping or losing precision silently. Snapshots
are recursively frozen and JSON-safe; a final null histogram bound represents
positive infinity. Event volume cannot increase retained collector state.

## Consequences

Applications keep one collector for their intended observation scope and attach
its bound record function directly to onEvent. Exporters run separately from
processing, choose metric names/units and handle cumulative snapshots, deltas,
process resets and saturation. No metrics library, exporter or network publication
dependency is introduced.

Metrics describe accepted process-local observations, not unique deliveries,
business effects, callback success or active work. Duplicate records count again;
missing shutdown events remain missing. No reset, restore, merge, sequence,
deduplication, gauge or durable checkpoint API is added. Recovery still uses the
existing receipt, intent and processing protocols.

No platform route, schema, migration, DDL or registry update is needed. The
protected-job example wires the observer without changing job versions. See the
[snapshot and exporter contract](../event-acknowledgment-node-metrics.md).

## Validation

Use Node compilation, generation, package/declaration inclusion and static
documentation checks. No tests, assertions or local test runs are included.
Hosted acceptance remains required for validation-before-mutation, histogram
boundaries, saturation, snapshot isolation and lifecycle counting before release.
