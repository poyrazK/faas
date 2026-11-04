# ADR-20261102124306: App health warns about memory pressure before an OOM kill

- Status: proposed
- Date: 2026-11-02
- Related: ADR-1155 (automatic heap capture on memory pressure), ADR-734 (app
  health), ADR-20261102091500 (worker health), ADR-20261023115741 (platform
  flags), spec §4.7 (billing is plan RAM, not sampled RSS)

## Context

An app that is running out of memory is usually found only after the kernel
kills it. ADR-1155 already built the signal: guest-init samples
`/proc/meminfo` every 2 s, and when `MemAvailable` falls to 15% of `MemTotal`
it sends one memory-pressure report (guest event `0x0A`), re-arming only once
availability recovers to 30%. vmmd relays the report to schedd, at most once a
minute per instance, and schedd queues a heap capture.

The report is only sent while a profile collector is active, and schedd keeps
nothing of it except the capture. An app without profiling, which is most
apps, never reports, and no one sees that an app spends its days at 90% of its
memory until it dies.

`MemAvailable` is the right signal: it counts reclaimable cache and reflects the
guest's real headroom. The host cgroup's `memory.high` and PSI do not: they see
the pages the guest has touched, which only grow.

## Decision

### 1. Every app reports, and the report says whether profiling is active

guest-init runs the watcher for every app, not only those with on-demand
profiling. The thresholds, the poll interval and the once-per-episode re-arm
are unchanged. The report gains `profiling` (whether a profile collector is
active), and vmmd forwards it to schedd as the new optional
`ReportMemoryPressureRequest.profiling_active`. A report from an older guest or
vmmd carries no value and is treated as `true`, which is what it meant before:
those only report while profiling.

### 2. schedd keeps a per-app rollup

schedd records each report in `app_memory_pressure`, one row per app:

| Column | Meaning |
|---|---|
| `window_started_at` | start of the current window, at most `api.AppHealthMemoryPressureWindow` (24 h) ago |
| `episodes` | reports within the window |
| `lowest_available_mb`, `total_mb` | the lowest headroom in the window, and the latest total |
| `peak_anon_mb` | the highest anonymous memory in the window |
| `last_at` | the latest report |

A report after the window has run out starts a new window. One upsert per
report, at most one a minute per instance, off the request path. The row is
deleted with the app. schedd then queues the heap capture as before, only when
the report says profiling is active.

### 3. App health shows it, behind a flag

With the `app_health_memory` platform flag (default off) released to the
account, app health adds a `memory` check:

- an episode within the last 24 h: warning, for example "Available memory fell
  to 23 of 256 MB (9%) 3 times in the last day; anonymous memory peaked at
  221 MB." Action `metrics`.
- otherwise: pass.
- a failed read: unknown.

Off, the response is unchanged.

## Consequences

- An app near its memory limit shows `degraded` days before an OOM kill, with
  the numbers that say how close it came.
- The guest change reaches an app when it next boots on a runtime release that
  carries it; until then only profiled apps report, as today.
- Plan RAM is what we bill (§4.7), so a later step can suggest a larger plan,
  or a smaller one for an app that never comes close, from the same rollup.
- Release compatibility: the guest JSON key and the proto field are new and
  optional. An old schedd ignores the field and tries a capture for every
  report; its existing plan, capability and opt-out checks skip apps without
  profiling, so deploy order (control plane before runtime) matters only for
  noise in the skip metric.

## Steps

1. This ADR, the guest change, the field, the rollup and the health check (one
   PR).
2. Release the flag to the owner's account; compare the warnings with real
   OOM kills.
3. A plan right-sizing hint in `app health` and `gregale debug status`.
