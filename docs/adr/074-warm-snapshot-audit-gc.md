# ADR-074 · Warm-snapshot audit + GC + ops surface

- **Status:** accepted
- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.
- **Date:** 2026-08-04
- **Issue:** #470 / PR C (extends PR #565 / PR A)
- **Supersedes:** the "Gregale flags + imaged GC" subset of the PR-C
  follow-up; this ADR documents the full close-the-loop surface, including
  the three audit kinds and the per-tier 2+2 GC floor.

## Decision

Ship a single PR C that closes the operations loop on the warm tier
writable by PR A. Seven atomic commits in merge order:

1. **ADR-074** (this file) — design doc.
2. **cmd/gregale flags** — `--warm-snapshot`, `--no-warm-snapshot`,
   `--warm-snapshot-min-requests N`, `--warm-snapshot-min-ms N`.
3. **pkg/wire metrics + vmmd observer + sched tier-aware wake** —
   `vmmd_warm_snapshot_errors_total` + `schedd_warm_snapshot_errors_total`
   (both prefixes exist: vmmd host-side capture path, schedd
   engine path — same `{reason}` label set on each), `vmmd_guest_init_duration_seconds`,
   `schedd_wake_snapshot_tier_total`. The vmmd DGRAM observer
   times full boot vs framework-ready. The sched tier-aware wake
   counter increments from inside `usableSnapshotForWake` based
   on the chosen tier.
4. **pkg/imaged GC 2+2 floor + warm storage keys** — `WarmSnapMemKey`
   and `WarmSnapVMStateKey` (already shipped by PR A); this PR extends
   `SnapshotForGC` with `AppWarmSnapshotEnabled` and replaces
   `perAppKeepCurrentPrevious` with `perAppKeepTierFloor`.
5. **pkg/sched engine audit + warm-capture ops counter** — emit
   `app.warm_snapshot_promoted` on capture success; increment
   `vmmd_warm_snapshot_errors_total{reason="vmm_call"}` on failure.
6. **pkg/imaged audit stale + apid audit disabled** — emit
   `app.warm_snapshot_stale` after the FC-version sweep; emit
   `app.warm_snapshot_disabled` from apid when `warm_snapshot_enabled`
   flips true → false.
7. **deploy/grafana/warm-snapshot.json** — four-panel dashboard.

## Why

PR A (#565) shipped the engine warm-capture hot-path, the vmmd
`WarmSnapshot` RPC, per-tier storage keys, the plan gate, and
ADR-071. The warm tier is now writable — but without PR C the feature
has gaps that block operations:

- apid already accepts `warm_snapshot_enabled` on PATCH, but `cmd/gregale`
  has no flags. Operators today have to curl PATCH.
- `pkg/imaged/loop.go::runGCTick` calls `perAppKeepCurrentPrevious` which
  keeps newest-2 per app with no tier awareness. After PR A, every Park
  writes a warm-tier row; without a tier-aware GC those rows accumulate
  forever for Free/Hobby plans that can never use them.
- Operators can grep `gregale audit-events --kind-prefix warm_capture_error`
  for failures (PR A emits that), but success / stale / disabled have no
  row.
- `vmmd_warm_snapshot_errors_total` is emitted to `/metrics` but no panel
  exists. Likewise `schedd_wake_snapshot_tier_total` does not exist yet
  (the wake tier selection happens in PR A's `usableSnapshotForWake`
  without a counter). PR C wires the wake-tier counter inside
  `usableSnapshotForWake` and adds `schedd_warm_snapshot_errors_total`
  for the engine-side capture-failure path.
- `vmmd_guest_init_duration_seconds` is absent — operators have no
  histogram of guest-init boot duration per app/runner.

PR C closes all five gaps. Net result: an operator can `gregale app myapp
--warm-snapshot --warm-snapshot-min-ms 1500`, watch the new Grafana panels
show warm-tier activity, see audit rows when captures happen, see GC
retention enforced nightly, and have a counter proof the wake path
prefers warm when allowed.

## Decisions

### 3.1 Per-tier GC floor: 2+2 (warm + init)

`pkg/imaged/gc.go::perAppKeepTierFloor` partitions rows by `appID`, then
by `tier`. If `appRows[0].AppWarmSnapshotEnabled == true`, the algorithm
keeps 2 newest warm + 2 newest init per app. If false, it keeps 2 newest
init only. The legacy `perAppKeepCurrentPrevious` (which keeps 2 newest
regardless of tier) is removed.

Why 2+2 and not 1+1? Two reasons. First, the wake path's worst-case
restore is a `cold_boot_fallback` (both tiers stale); keeping an extra
prev row halves the cold-boot rate. Second, retention is bounded by
disk budget: 2 warm @ ~50 MB each + 2 init @ ~150 MB each = 400 MB per
app. At 100 Scale apps that's 40 GB — well under the 130 MB
`snapshot_fleet_avg_mb` target.

Why not 2+4? Init tier rows are larger (full mem + vmstate) and fewer
apps need 4 init rotations. 2+2 hits the 350 ms warm-wake budget
(§6.3) without excess storage.

### 3.1a Rollback retention amendment (2026-09-07)

The rollback path now retains the newest three deployment generations per
app (the live deployment plus two previous releases). For warm-enabled apps,
both init and warm rows remain eligible for fast restore in each protected
generation; when warm snapshots are disabled, only init rows are retained.
Superseded-deployment notifications leave the shared app layer and snapshot
tiers intact. Nightly GC evicts rows outside the generation window and removes
an app layer only after that deployment has no non-stale snapshot rows left.
This bounded generation window supersedes the numeric 2+2 floor above while
preserving its tier-safety intent and the cold-boot fallback for older releases.

### 3.2 Audit subject shape: `&app.AccountID`

All three audit kinds (`app.warm_snapshot_promoted`, `app.warm_snapshot_stale`,
`app.warm_snapshot_disabled`) emit with `subject = &app.AccountID`. This
matches `app.updated`'s shape at `cmd/apid/handlers_ext.go:569`.

This **diverges** from `app.characterized`'s shape (`nil` subject at
engine.go:1384). The discrepancy is intentional: `app.characterized`
fires on the wake path before the audit emitter knows the account
(unless we plumb `app` through `e.audit`); warm-snapshot events always
have the parent app in scope, so we use the canonical account-scoped
form. Account-scoped listing via `gregale audit-events --account-id <uuid>`
is the operator UX contract; `app.characterized` is grep-for-only.

### 3.3 5th gate (request-count) deferred

`apps.warm_snapshot_min_requests` (CHECK 1..100, default 5) ships in
PR #525 but has no data source for the engine to consult. Today's
instances table has no successful-request counter. A future ADR-073
owns the counter-source decision (e.g. `instance_wake_request_count`
tracked by the gatewayd per-instance LastSeen map). PR C ships only
the 4 gates PR A documents (warm-snapshot enabled, plan gate,
framework-ready stamped, min-ms elapsed). The column stays dormant.

### 3.4 Dashboard panel set

`deploy/grafana/warm-snapshot.json` exports four panels mirroring
`faas-fleet.json`'s schemaVersion:

| ID | Type | Query | Purpose |
|---|---|---|---|
| 1 | timeseries | `sum by (reason) (rate({vmmd,schedd}_warm_snapshot_errors_total[5m]))` | Failures per reason (both prefixes) |
| 2 | timeseries | `histogram_quantile(0.50/0.95, sum by (le, app) (rate(vmmd_guest_init_duration_seconds_bucket[5m])))` | Boot duration p50/p95 |
| 3 | timeseries | `sum by (tier) (rate(schedd_wake_snapshot_tier_total[5m]))` | Wake tier mix |
| 4 | stats | `count(snapshots{tier="warm"})` / `count(snapshots{tier="init"})` | Population by tier |

### 3.5 Histogram bucket set

`vmmd_guest_init_duration_seconds` uses the buckets spec §6.3 verbatim:
`{0.05, 0.1, 0.2, 0.3, 0.35, 0.5, 0.8, 1, 1.5, 3, 5}`. The consecutive
0.3 / 0.35 pair is intentional: the 350 ms warm-wake budget needs tight
resolution near 0.35 to draw the boundary between "warm restore" and
"warm restore that missed the §6.3 SLA". Coarser buckets would lose
visibility into the regression case.

## Capture path audit emit (PR C.4)

`pkg/sched/engine.go::captureWarmSnapshotLocked` (PR A) emits:

- **Success**: `e.audit.Emit(ctx, "app.warm_snapshot_promoted", &app.AccountID,
  map[string]any{ "app_id": app.ID, "snapshot_id": snapID, "deployment_id": depID,
  "warm_min_requests": app.WarmSnapshotMinRequests, "warm_min_ms": app.WarmSnapshotMinMs })`.
  Subject is `&app.AccountID` per §3.2. The payload shape matches
  `app.characterized` (engine.go:1384) so an operator tailing events
  greps one shape.
- **Failure**: the existing `transitionWithKind(ctx, ins.ID, ins.AppID,
  state.StateStopped, "warm_capture_error", "warm_snapshot_failed")`
  (engine.go:2855) plus `e.ops.WarmSnapshotErrors("vmm_call").Inc()`.

## GC algorithm (PR C.3)

`perAppKeepTierFloor` is the F1 body rewrite. Pseudocode:

```go
func perAppKeepTierFloor(rows []state.SnapshotForGC) []deleteTarget {
    byApp := groupBy(rows, func(r state.SnapshotForGC) string { return r.AppID })
    var drops []deleteTarget
    for _, appRows := range byApp {
        enabled := appRows[0].AppWarmSnapshotEnabled
        byTier := groupBy(appRows, func(r state.SnapshotForGC) string { return r.Tier })
        for tier, tierRows := range byTier {
            keep := 0
            if enabled && tier == "warm" {
                keep = 2
            } else if tier == "init" {
                keep = 2
            } else {
                keep = 0
            }
            sortByCreatedAtDesc(tierRows)
            for _, r := range tierRows[keep:] {
                drops = append(drops, deleteTarget{ID: r.ID, Tier: r.Tier, DeploymentID: r.DeploymentID, AppSlug: r.AppSlug})
            }
        }
    }
    return drops
}
```

`loop.go::deleteSnapshotsAndFiles` looks up `deleteTarget.Tier` and
deletes the right key pair:

- `warm` → `WarmSnapMemKey(dep)` + `WarmSnapVMStateKey(dep)`.
- `init` → `SnapMemKey(dep)` + `SnapVMStateKey(dep)`.
- Always delete `sched.AppLayerKey(slug, dep)` (per-app ext4 layer;
  shared across tiers).

## Rejected alternatives

- **Keep 1 warm + 1 init.** Rejected: a single warm row gives no
  fallback if the row is GC'd by another path (e.g. account
  delete-without-park). 2+2 leaves one redundant row.
- **Per-account floor instead of per-app.** Rejected: one customer's
  Scale app with 50 concurrent instances needs 50 rows; per-account
  boundary would over-retain for one-tenant-per-app accounts and
  under-retain for multi-tenant Scale accounts.
- **Audit `app.warm_snapshot_promoted` with subject `nil`** (mirror
  `app.characterized`). Rejected: account-scoped listing is the
  operator UX contract; `nil` would lose the grep affordance.
- **Implement the 5th gate (request-count).** Rejected: no
  successful-request counter exists today. This is a separate
  ADR-073 decision.

## Critical reference files

| Concern | Path |
|---|---|
| Per-tier GC algorithm | `pkg/imaged/gc.go` (F1 body rewrite) |
| runGCTick call site | `pkg/imaged/loop.go:193` |
| Delete with tier-aware keys | `pkg/imaged/loop.go:296` |
| SnapshotForGC SQL projection | `pkg/state/pgstore.go:5821` |
| Warm storage keys | `pkg/state/keys.go:75` (PR A) |
| Engine capture path | `pkg/sched/engine.go:2718` (PR A) |
| Wake tier selection | `pkg/sched/engine.go:3129` (PR A) |
| apid audit emit | `cmd/apid/handlers_ext.go:569` + new disabled branch |
| Gregale flags | `cmd/gregale/commands2.go:94` (overload) |
| Metrics | `pkg/wire/metrics.go:93+` (PR A), `[new]` for two vmmd series |
| Dashboard | `deploy/grafana/warm-snapshot.json` (new) |
