# ADR-159 · Overlapping initial wake bursts

- **Status:** accepted
- **Date:** 2026-09-06

## Context

The gateway currently restores one VM through `EnsureWake`, then asks for
additional burst capacity. SSD event timelines show the second boot beginning
23–35 ms after the first completes. Waiting for both before forwarding turns
these boot durations into a serial critical path. Forwarding early alone did
not establish a p95 improvement and often left the second VM unused.

## Decision

Add a bounded `desired_instances` hint to `EnsureWakeRequest`. The gateway
samples authenticated, rate-limited request pressure already accumulated behind
its cold-start gate, divides by the existing plan concurrency bound, and clamps
to the app/plan instance ceiling and `ScaleUpMaxBurstPerTick`. No collection delay
or additional always-resident capacity is introduced.

The existing scheduler wake coordinator remains the owner of a shared lifecycle.
Its leader uses the hint captured at entry. Followers inherit the actual ready
results; later or larger demand still uses ordinary burst reconciliation. An
existing running instance keeps the existing fast path.

For a multi-instance hint, the first wake signals an internal readiness channel
after its ledger admission, immutable boot-spec construction, and layer
verification, outside the per-app lock. Only then may bounded sibling admissions
start. They use the existing burst-continuation context: only the first wake's
scale-out cooldown is shared; placement, app/plan caps, RAM/CPU ledger,
rate limits, owner checks, secrets, sidecars, signatures, and each VM's runtime
readiness remain enforced by their existing owners. No unverified target is
published. Every started admission is awaited on the bounded shared lifecycle.

The response retains its primary instance fields and adds independently ready
siblings. If an admitted VM fails but another succeeds, successful capacity is
returned. If no VM succeeds, the admission error is preserved. Failed VM cleanup
uses the existing scheduler paths. The gateway caches all returned targets and
marks the initial batch as its active capacity generation, preventing a partial
RUNNING notification from launching duplicate local expansion.

The request/response fields are additive. Old servers return one target and old
clients ignore siblings. Non-capacity-aware adapters and preview-scope wakes keep
their existing paths. Every VM is still scheduler-owned and billed normally.

## Validation

Regression tests hold both VM RPC completions and require both to have started;
they also hold first-layer verification and prove no sibling is admitted early.
Tests cover app caps, existing-instance reuse, partial failure cleanup, canceled
leader/shared follower lifetime, RPC target fields, legacy adapters, queued
pressure, and duplicate-expansion prevention. Physical concurrent snapshot
restores and post-test leak checks must pass on the SSD host before rollout.
Full wake p95 under burst/public traffic remains an acceptance gate; this design
alone does not assert that the 350 ms target is met.

## SSD canary evidence

On 2026-09-06, 20 concurrent restore pairs passed physical isolation and leak
checks (40 unique guest UUIDs). The live Node22 fixture then served 50/50
isolated snapshot wakes and 300/300 burst requests successfully, starting each
wave with zero live VMs. Isolated CP-to-SSD HTTP p95 was 278.5 ms. Across three
100-request bursts, the sibling began 101–124 ms before the primary completed;
first boot start to both completions was 155–221 ms. Each pair served 50 requests
per VM. End-to-end burst p95 remained 676–865 ms, so this confirms overlap but
not a statistically established end-to-end improvement or the 350 ms burst SLO.

A request selecting a sibling uses that VM's wake ID for response correlation;
ordinary warm responses do not acquire a historical wake header.
