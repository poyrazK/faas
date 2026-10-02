# ADR-459: Scope JIT settings to the capacity projection

- **Status:** accepted; fleet enable still requires native qualification
- **Date:** 2026-10-02
- **Milestone:** Multi-region compute placement
- **Related:** ADR-422, ADR-463, ADR-464

## Problem

Linux checks on the GCP internal test node exposed expensive PostgreSQL JIT
compilation in `service_capacity_snapshot_inputs()`. On the same small fixture,
three calls with JIT enabled took 12.8–15.8 seconds; interleaved calls with JIT
disabled took 15.4–33.2 ms. These are diagnostics on a shared, CPU-limited node,
not production throughput or native hardware qualification.

The projection joins declarations, residents and physical budgets. Its planner
estimates can trigger JIT even for a small fleet. Resource admission evaluates
this projection while holding the capacity policy lock. Compiler work therefore
delays both the mutation and other admissions; correct capacity arithmetic is
insufficient if the boundary takes seconds to evaluate.

## Decision

Set `jit=off` on `service_capacity_snapshot_inputs()` through an append-only
migration. PostgreSQL applies the setting only during the function call and
restores the caller's setting afterward. Keep the projection query, regional
solver, resource costs, policy state and serialized admission guard unchanged.
Other queries retain their session and cluster settings.

Both migration directions take the capacity policy lock. Up and replay replace
only this function setting; Down resets it while preserving the enabled capacity
policy, admission logic and unrelated function settings. Downgrade restores the
caller's JIT behavior and can reintroduce the diagnostic latency.

## Validation

A populated PostgreSQL regression compares projection values with JIT disabled
and with a caller that forces JIT at zero cost thresholds. It checks caller
settings are restored, unsafe regional intent remains refused, and policy-locked
replay/down/up retain the enabled certificate and unrelated function settings.

Native host-loss/routing, admission throughput at representative workload counts,
`test-metal` and `leakcheck` remain fleet-enable gates. The internal GCP node uses
nested virtualization and does not satisfy those native hardware gates.
