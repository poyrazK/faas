# ADR: Worker recovery holds deterministic boot refusals

Date: 2026-10-27
Status: Accepted
Amends: [durable worker-pool recovery](20261017221000-durable-worker-recovery.md)

## Context

Durable worker recovery retries a failed admission with a 5–300 second
exponential backoff and resets it only when the desired revision changes. Some
boot refusals cannot succeed until that revision changes: the image serves HTTP
while the app declares a worker (`execution_mode_conflict`), the image is built
for another architecture, the workload binds loopback only, or it breaks the
stateless-only rule. On production-us rc.297 one such worker was refused 293
times a day for 19 days (2,083 cold boots). Every boot cost a node a VM start
and the app stayed active with no new signal for its owner (Hunt 14 H14-98).

## Decision

When the failed attempt's error is one of these refusals
(`execution_mode_conflict`, `app_arch_mismatch`, `app_loopback_bound`,
`stateless_only_violation`), the next attempt waits
`api.WorkerRecoveryDeterministicRefusalHoldSeconds` (one hour) instead of the
backoff. Every other failure keeps the 5–300 second backoff. A new deployment,
manifest, resource, runtime, ownership or scaling-policy change still resets the
lease at once, as today. The hold uses the existing `retrying_startup` status
and lease columns; no schema change.

## Consequences

A stuck worker of this kind boots about 24 times a day instead of about 290.
A fix that does not change the revision (a secret, for example) waits up to an
hour; these four refusals come from the image or its manifest, not secrets.
Crashes, OOMs and timeouts, which can be transient, are unaffected.
