# Financial-model addendum: sidecar RAM math (issue #463 / ADR-069)

**Status (issue #463 / ADR-069 §"Financial-model addendum"):** in-repo
record of the financial model spreadsheet's scenario columns for
sidecar deployments. The xlsx is offline (lives on the reference node
only, git-ignored per CLAUDE.md), so this file is the canonical
**in-repo** source of truth until the corresponding spreadsheet row is
patched. The two records MUST agree.

## Billable-RAM formula

```
per_instance_billable_mb = plan.RAMMB + Σ(sidecar.ram_mb) + PerVMOverheadMB
```

- `plan.RAMMB` — the customer's plan tier RAM quota (Free 128 /
  Hobby 256 / Pro 512 / Scale 1024). Source: `pkg/api/limits.go`.
- `Σ(sidecar.ram_mb)` — sum of per-helper `ram_mb` over the
  deployment's `companions[]` array (legacy `sidecars[]`). The customer-supplied value
  per sidecar, validated at `pkg/api/dto.go::Sidecar.Validate`
  (16 MB floor, plan RAM ceiling per sidecar). Source:
  `pkg/api/limits.go::SidecarRamMBMatrix`.
- `PerVMOverheadMB = 8` (`pkg/api/limits.go:848`) — the per-VM
  overhead. The +8 MB lives on the **host-side parent cgroup
  scope** (`pkg/fcvm/cgroup.go::writePlanCgroup`) and is shared
  across all workload children of an instance. Per-workload
  cgroup leaves (PR-B AC #4) take the customer's declared
  `ram_mb` only — no +8 surcharge on each workload.

This is the shape `pkg/api/limits.go::BillableRAMMBWithSidecars`
implements (`pkg/api/limits.go:1879`). Sibling helper
`BillableRAMMB` (no-sidecar variant, `pkg/api/limits.go:1859`)
is `plan.RAMMB + PerVMOverheadMB`. Both consumers
(schedd admission, schedd placement, schedd reaper, schedd
instancestats poller, schedd gRPC, apid) go through these two
helpers so the only place the overhead constant lives is
`PerVMOverheadMB`.

## Expanded companion capacity

ADR-223 raises the bound to five helper entries: one init helper and up to
four long-running companions. The formula is unchanged, but all five RAM
reservations are included. For example, five helpers configured at 64 MB
each reserve `plan.RAMMB + 320 + 8` MB per instance. The historical 0/1/2
rows below remain useful examples; they are no longer maximum-capacity
scenarios. A deployment at the per-helper ceiling could reserve up to
`plan.RAMMB + 2,560 + 8` MB. The offline financial-model workbook must add
the five-helper scenario before the old table is used as a maximum.

## Scenario columns

The following 0/1/2-helper rows are illustrative and do not show the new
five-helper ceiling.

| Plan | Plan RAM (MB) | Helper count           | Σ helper.ram_mb | Per-VM overhead | Per-instance billable (MB) | 30-day @ 720h × 1 instance (GB-h) | Plan ceiling (GB-h) | Overage (€0.01 / GB-h) |
|------|---------------|--------------------------|------------------|-----------------|----------------------------|-----------------------------------|---------------------|------------------------|
| Free | 128           | 0                        | 0                | 8               | 136                        | ~92                                | 5 (included)        | ~€0.87                 |
| Hobby | 256          | 0                        | 0                | 8               | 264                        | ~178                              | 50 (included)       | ~€1.28                 |
| Hobby | 256          | 1 (init 64 MB)           | 64               | 8               | 328                        | ~221                              | 50 (included)       | ~€1.71                 |
| Hobby | 256          | 2 (init 64 + sidecar 64) | 128              | 8               | 392                        | ~265                              | 50 (included)       | ~€2.15                 |
| Pro   | 512          | 0                        | 0                | 8               | 520                        | ~351                              | 250 (included)      | ~€1.01                 |
| Pro   | 512          | 1 (init 64 MB)           | 64               | 8               | 584                        | ~394                              | 250 (included)      | ~€1.44                 |
| Pro   | 512          | 2 (init 64 + sidecar 64) | 128              | 8               | 648                        | ~437                              | 250 (included)      | ~€1.87                 |
| Scale | 1024         | 0                        | 0                | 8               | 1032                       | ~696                              | 1500 (included)     | (under)                |
| Scale | 1024         | 1 (init 64 MB)           | 64               | 8               | 1096                       | ~740                              | 1500 (included)     | (under)                |
| Scale | 1024         | 2 (init 64 + sidecar 64) | 128              | 8               | 1160                       | ~783                              | 1500 (included)     | (under)                |

**GB-h columns** assume a single instance at `max_concurrency`
floor for 30 days × 24 h = 720 h. Multiply by `max_concurrency`
for the cluster-level rollup.

**Decimal-vs-binary divisor** — the model uses the binary
divisor (1 GB = 1024 MB) per ADR-039 and the existing
`pkg/meter/sampler.go` math. Hobby customers on a sidecar
deployment **exceed their 50 GB-h included ceiling** under any
sidecar usage; Pro customers exceed their 250 GB-h ceiling under
any sidecar usage. Scale usage depends on configured helper RAM and
concurrency; these historical two-helper rows do not bound the new cap.

## Verifications (sheet rows, reference node only — must mirror this table)

1. Hobby 50 GB-h included ceiling is exceeded under any
   sidecar usage (1-init row at ~221 GB-h ≫ 50 GB-h).
2. Pro 250 GB-h included ceiling is exceeded under
   `sidecars >= 1` (~394 GB-h at 1-init, ~437 GB-h at 2-sidecar).
3. Recalculate Scale overage at the five-helper capacity and the
   configured `max_concurrency`. The old two-helper examples already
   show why companion RAM is not free; the new cap increases the
   possible reservation, so the offline workbook needs a matching row
   before customer-facing estimates use a maximum-capacity scenario.

## Source-of-truth pointer

The in-code implementation is
**`pkg/api/limits.go::BillableRAMMBWithSidecars(ramMB int, sidecarMBs []int) int`**
(`pkg/api/limits.go:1879`). Unit-pinned by
**`pkg/api/limits_test.go::TestBillableRAMMBWithSidecars`**
(`pkg/api/limits_test.go:496`) with the 6 cases
(no-sidecars / one-init / two-sidecars / empty-slice / zero-skipped
/ scale-two-sidecars).

A future PR may extract a per-sidecar `ram_mb` lookup from the
persisted `deployments.sidecars` jsonb; today the billed value is
re-derived from the deployment row at meter time
(`pkg/meter/sampler.go`).

## Operationally what needs to happen on the reference node

The spreadsheet row lives in the financial model spreadsheet on the reference node.
The row must contain, at minimum:

- A `per_instance_billable_mb` column with the per-plan ×
  per-sidecar-count matrix above.
- A `30-day GB-h` column with the projection at
  `max_concurrency = 1` (the per-instance view).
- A `30-day GB-h × max_concurrency` column with the cluster-level
  rollup; this is what tips Hobby/Pro/Scale into overage.

CI does NOT enforce the workbook row because the workbook is
offline (per CLAUDE.md "the spreadsheet is absent from the
repo"). The reviewer checks during PR review that the reference-node
operator has committed a matching row before merge.

## Why a doc, not the xlsx

CLAUDE.md: *"the financial model spreadsheet — business numbers.
Never contradict it in code or docs."* Code that materially
changes billable math (issue #463 PR-A added
`BillableRAMMBWithSidecars`) MUST have an in-repo record, in
the same direction as the spreadsheet. This file IS that
record. The PR is mergeable when this file lands; the
spreadsheet row catch-up is tracked out-of-band.
