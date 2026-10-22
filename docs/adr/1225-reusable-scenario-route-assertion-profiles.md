# 1017: Reuse scenario route assertion profiles

Date: 2026-10-13

Status: Accepted

## Context

Scenario load phases already evaluate per-request latency budgets and sustained
endpoint recovery deadlines. Users need to reuse these requirements across
outage phases and scenarios without duplicating budgets or writing fixture-owned
assertion commands.

## Decision

Add manifest-level named `assertion_profiles` containing `healthy_routes` and
`recovery_deadlines`, referenced by phase-level `assertions`. Resolve profiles
before outage-cycle and fault-sweep expansion into existing `step_thresholds`
and `step_recovery` evaluators. Copy mutable budgets independently for each use.

Require healthy route latency, explicit success/error rate and minimum samples.
Use existing recovery-window, timing, stability and endpoint validation. Reject
unknown references and conflicting route budgets rather than silently overriding
requirements. Validate unused profile definitions as well as each referenced
phase's endpoint names, fault placement and duration.

Preserve referenced profile names in phase evidence and render them in terminal
and HTML output. JSON and JUnit retain existing measured threshold and recovery
results. Replay hashes include effective budgets and referenced profile names.

## Consequences

Existing manifests and evaluators remain compatible. Profiles are scoped to one
manifest and address named HTTP requests, not dependency internals or arbitrary
application metrics. No production retry, breaker or concurrency policy changes.
No new native qualification claim accompanies this configuration feature.
