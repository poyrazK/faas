# ADR-20261121200409: Operator wake-latency report

Status: Accepted

Extends ADR-20261115175927 (operator console routes) and ADR-064 (typed wake
events).

## Context

The <350 ms snapshot restore interval (spec §14) is the product's main
latency promise. Operators could see it only as per-node p50/p95/p99 from
Prometheus (`GET /v1/admin/obs/nodes/wake-latency`), or one wake at a time
through the wake timeline (`GET /v1/admin/obs/wakes/{wake_id}`). Neither
answers the on-call questions: which wakes were slow and in which phase, how
often wakes cold booted instead of restoring and why, and which apps keep doing
it. The answers are already recorded per wake in the `events` table:
`wake.boot_started` (planned method, cold reason, schedd's pre-RPC spans),
`wake.boot_completed` (actual method, start and finish, fallback reason,
schedd's post-RPC spans), `wake.boot_failed`, and vmmd's
`wake.restore_breakdown` / `wake.cold_boot_breakdown` phase timings, plus
schedd's `wake.thaw_breakdown` for frozen-tier thaws.

## Decision

One operator session read:

| Route | Returns |
| --- | --- |
| `GET /v1/admin/obs/wake-latency?window=1h&node=&app_id=` | `ObsWakeLatencyReport` |

The report reads each of the six wake event kinds over the window through a
new optional store read, `state.EventsByKindReader.ListEventsByKindSince`
(`kind = $1 AND at >= $2 ORDER BY at DESC`, a backward scan of the existing
`events_kind_at_idx`). It reads at most `ObsWakeLatencyEventsPerKind` (20,000)
rows per kind; a kind that hits the cap is named in `truncated`, and the
figures then cover only the newest part of the window. `window` is a duration
from 1m to 24h (default 1h). `node` is a node name; `app_id` filters by app.

`pkg/apid/wakelatency` joins the rows on `(wake_id, instance_id)` and reports:

- totals: wakes, restores, cold boots, thaws, failures, restore→cold-boot
  fallbacks and restores over the 350 ms target
  (`ObsWakeLatencyRestoreTargetMS`);
- per method and per node: count and nearest-rank p50/p95/p99/max;
- phases: every `*_ms` span from the breakdowns and schedd's pre/post-RPC
  spans, ranked by p95, with `total_ms` left out;
- cold-boot reasons (from `cold_reason`, or `restore_fallback`) and failure
  reasons;
- the slowest wakes (`ObsWakeLatencySlowestMax`, 25), each with its longest
  phase;
- the apps with the most cold boots (`ObsWakeLatencyAppsMax`, 25).

A wake's duration is schedd's boot window, `started_at` → `completed_at` on
`wake.boot_completed`; a thaw's is `total_ms` on `wake.thaw_breakdown`. Boots
with the `platform.snapshot_refresh` trigger are counted in
`platform_refreshes` and left out of everything else, because no customer
waited on them.

## Consequences

- The console gets a Wake latency page on call, linking each slow wake to its
  timeline and each app to its detail.
- No migration. One indexed read per kind per request, at most 120,000 small
  rows, aggregated in apid. The page is operator-only and read on demand.
- The Prometheus per-node quantiles stay. They cover gateway-measured wakes
  over Prometheus retention; this report covers schedd and vmmd timings and
  can name the wake and the phase.
- The route is operator-only and excluded from the public OpenAPI spec like
  the other `/v1/admin/obs/*` routes.
