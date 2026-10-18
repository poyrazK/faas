# ADR-1249: Bounded passive management MCP rollout watch

## Status

Accepted. Extends ADR-1244 through ADR-1248.

## Decision

Add one read-only watch tool for an exact app/environment deployment. Combine
existing deployment/build reads, scoped passive health and native worker
residency observations within a bounded deadline. Validate every identity and
recheck consent/environment identity while observing. Do not add API mutations,
active probes, background jobs or a persisted watch cursor.

Acceptance, live artifacts, fresh exact-serving health and completion remain
separate. Native completed service handoff and request traffic/state are required
for their completion claims. Worker completion means resident generation
convergence only, never queue processing or desired replica satisfaction.
Unsuccessful terminal deployments return immediately; otherwise return the last
validated snapshot when the watch budget expires. Facts are sequential reads,
not a transactionally atomic view. Recoveries remain explicit reviewed actions.
