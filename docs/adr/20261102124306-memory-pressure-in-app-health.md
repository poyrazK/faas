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
3. A plan right-sizing hint in `app health` (amendment 1) and `gregale debug
   status` (amendment 2).

## Amendment 1 (2026-11-04): how much RAM the app needs

Step 3, as a RAM suggestion in the `memory` check. The check's warning (reason
`memory_pressure`) now adds how much RAM would have left
`api.ProfileMemoryPressureRearmAvailablePercent` (30%) of the guest's memory
available at the rollup's low point, `api.RightSizeMemory`:

```
need = (ram_mb − guest MemTotal) + ⌈(MemTotal − lowest available) / 0.7⌉
```

The result is rounded up to `api.MemoryRightSizeStepMB` (64 MB) and is always
above the current `ram_mb`. The guest kernel's share (`ram_mb` − MemTotal)
stays the same whatever the app's size. The suggestion then reads one of three
ways:

- It fits the account's plan: "Raise ram_mb from 512 MB: about 704 MB would
  have kept 30% free, and the pro plan allows up to 1024 MB." The action is
  `configuration`.
- It needs a larger plan: the check names the smallest plan whose RAM allows
  it.
- No plan allows it: the check says so and points at in-memory state.

The check gives no suggestion when the plan is unknown; the background history
collector, for example, passes none.

The rollup only records episodes at or below 15% available, so it cannot show
that an app is too large. A suggestion to shrink would need sampled guest
memory, which the rollup does not hold, so this step does not suggest one.
`gregale debug status` gains the suggestion in a later change; it needs a new
response field (amendment 2).

### Fix: the check codes are in the API enum

The worker and memory checks (ADR-20261102091500 and this ADR) emit check
codes `replicas`, `queue`, `backlog`, `dead_letters` and `memory`. The OpenAPI
`AppHealthCheck.code` field is a closed enum that did not list them, and the
generated Python SDK raises on a value outside the enum, so releasing either
flag would have broken Python clients that read app health. The enum now lists
them, the `reason` enum lists `memory_pressure`, and the generated Node and
Python models match. Both flags were still off everywhere.

## Amendment 2 (2026-11-05): the suggestion in debug status

`GET /v1/apps/{slug}/debug/status` gains an optional `memory_pressure` object
(`api.DebugStatusMemory`) and `gregale debug status` shows it: a "Memory
pressure" line with the rollup's low point, episode count and newest episode,
and the suggestion under "Memory:" after any OOM remedy.

- It is the same evidence and arithmetic as the memory check:
  `apphealth.DebugMemory` reads the rollup and calls `api.RightSizeMemory`, and
  the sentence is `api.MemoryRightSizing.Advice`, which the memory check now
  uses too.
- It is present only for accounts the `app_health_memory` flag releases, when
  the rollup's newest episode is within `api.AppHealthMemoryPressureWindow`
  and the plan is known. A failed rollup read leaves it out; the status never
  fails for it.
- It does not change the status verdict. The dashboard's status card is
  unchanged.
- The field is new and optional (preview spec only), so older CLIs and SDKs
  ignore it. The generated Node and Python preview models include it.

