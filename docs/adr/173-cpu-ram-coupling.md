# ADR-173 — Plan CPU/RAM coupling

Status: Accepted, 2026-09-11. Extends ADR-014 and ADR-152.

## Context

Gregale exposes two resource dimensions: the guest-visible vCPU topology and
the app's sustained cgroup CPU quota. Before this decision, the API exposed
the former only through `effective_limits.guest_vcpus`, so clients could send a
RAM value without seeing or asserting the topology that protects cold-wake
latency.

The existing v1 runtime contract is plan-derived topology: Free, Hobby, and Pro
use two guest vCPUs; Scale uses four. ADR-152 separately made sustained CPU
configurable at 250m, 500m, or 1000m. These dimensions must stay distinct.

## Decision

`pkg/api/limits.go` owns a canonical plan resource-shape table:

| Plan | Canonical RAM (MB) | Guest vCPU |
|---|---:|---:|
| Free | 128 | 2 |
| Hobby | 256 | 2 |
| Pro | 512 | 2 |
| Scale | 1024 | 4 |

`POST /v1/apps` accepts an optional `vcpu` assertion. When supplied, the
`(ram_mb, vcpu)` pair must match the table and the API returns a 422
`invalid_cpu_ram_pair` problem otherwise. The error includes the literal
`invalid (ram_mb, vcpu) pair for plan` so CLI and SDK clients can render the
same remediation. When omitted, vCPU remains the current plan default and
existing RAM/profile behavior is unchanged.

The assertion is intentionally create-time only. vCPU is not persisted on
`apps` and is not a per-app override in v1; read surfaces expose the effective
plan topology through `vcpu` and `effective_limits.guest_vcpus`. Existing
database checks for positive RAM and the closed sustained-CPU set remain the
schema backstop. A cross-table SQL check is not added because the account plan
is owned by a separate row and the runtime source of truth is the plan table.

The Go, Node, and Python SDKs expose `CreateAppRequest.vcpu`,
`AppResponse.vcpu`, and `AccountLimits.vcpu`. The CLI exposes `gregale deploy
--vcpu` and prints the effective guest topology in `gregale app <slug>`.

## Consequences

- Invalid explicit CPU/RAM assertions fail before app creation or deployment
  work, with a stable RFC 7807 code.
- Existing apps, custom RAM values, and named RAM/CPU profiles continue to
  work when callers omit `vcpu`.
- Host admission and Firecracker boot remain unchanged because topology is
  still plan-derived.
- A future per-app topology change requires a new ADR, state migration, and
  scheduler/vmmd wiring rather than silently extending this assertion field.
