# ADR-152 — Configurable sustained CPU per app

Status: Accepted, 2026-09-05. Extends ADR-044.

## Context

Gregale already lets an app select memory below its plan ceiling, but CPU is
always derived from the account plan. Every current plan resolves to 1000
millicores even though the guest sees two or four vCPUs. Customers cannot
right-size CPU-bound behavior, and the API cannot distinguish configured CPU
from guest topology.

## Decision

Apps persist `cpu_millicores` as a closed set of 250, 500, or 1000. Omitted
values and migrated rows default to 1000, preserving existing behavior. Create
and update validate the closed set before persistence.

The scheduler sends the value in `vmmd.AppSpec` on cold boot, snapshot restore,
and live-migration adoption. vmmd converts millicores to cgroup v2 `cpu.max`
using the plan's existing period. Guest vCPU count and `cpu.weight` remain plan
derived: vCPU count describes guest topology, CPU quota controls sustained
execution, and weight controls relative service under contention.

The app response exposes `configured_resources` and keeps the existing
`effective_limits` block. The CLI accepts `--cpu-millicores`; the dashboard
shows the configured shape. Changes take effect when an instance next boots or
restores, matching the existing `ram_mb` update contract.

## Consequences

- Existing apps retain a 1000 millicore quota.
- 250m and 500m shapes can constrain workloads without changing billing.
- The scheduler still accounts plan-derived guest vCPUs, so this release does
  not increase host admission density.
- CPU shapes above 1000m require a separate plan-cap and pricing decision.
