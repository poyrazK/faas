# ADR-967: On-demand CPU and heap profiling

- **Status:** accepted for internal implementation; native acceptance pending
- **Date:** 2026-10-10
- **Decision:** Ship dormant guest collectors that the control plane can arm
  for a bounded CPU and/or heap capture on one running instance without a
  redeploy. apid queues the capture in Postgres, schedd claims it and asks the
  owning vmmd, vmmd delivers the request on guest-init's control listener and
  returns the collectors' unparsed pprof profiles, and apid parses, merges and
  renders them. Continuous profiling (ADR-819) also gains heap profiles.
- **Why:** Continuous CPU profiles answer "what got slower across deploys";
  they cannot answer "what is this instance doing right now" or "where is this
  memory leak" without a redeploy that changes the behavior being
  investigated. CPU-only collection also leaves memory growth unexplained.

## Context

ADR-819 collects CPU only, and only for deployments whose manifest sets
`profiling.enabled`. Enabling it disables warm snapshots and init-snapshot
reuse (the checkpoint handshake must drain running profilers), so it cannot be
the default. Customers need to profile a misbehaving instance as it is.

## Decision

### Dormant collectors

imaged sets `profiling_on_demand` in a deployment's guest manifest when the
operator enables `FAAS_PROFILING_ON_DEMAND=1` and the account's plan includes
profiling. guest-init then starts the local bridge (127.0.0.1:9191) with
`enabled=false` and stamps the managed Node and Python preloads, exactly as
continuous profiling does. Go apps that call `guestprofiling.Start` are
covered too.

A dormant collector loads no profiler and polls the bridge once per second;
an armed capture starts within one poll. Because nothing runs, a dormant
bridge answers a checkpoint immediately, and on-demand alone does **not**
enable the before_checkpoint handshake or disable warm/reused snapshots.

### Capture flow

1. `POST /v1/apps/{slug}/profiles/captures` inserts a `queued` row in
   `profile_captures` (one active capture per app, 60 per account per hour)
   and emits `pg_notify('profile_capture')`. apid never calls schedd or vmmd.
2. schedd's drain claims queued rows with `FOR UPDATE SKIP LOCKED`, runs each
   capture in its own goroutine (at most four per schedd) and picks the
   requested or earliest-started RUNNING instance. Parked apps are not woken:
   a fresh process would not show the state under investigation.
3. vmmd's `CaptureProfile` RPC holds the instance's in-flight activity, so
   the idle reaper does not park it mid-window, and sends message type 7 on
   guest-init's control listener (vsock 1024).
4. guest-init switches the bridge to a fresh capture epoch with the requested
   kinds. Collectors restart, profile for the window, and stop and flush when
   the bridge disables the epoch at its end. After a 3 s grace the reply
   carries up to 8 profiles (3 MiB total). Continuous configuration then
   resumes under a new epoch; continuous CPU uploads in a capture epoch are
   forwarded as usual, so continuous coverage has no gap.
5. schedd stores the profiles unparsed in `profile_capture_data` and marks the
   capture `ready`, or `failed` with a customer-facing reason. Captures stuck
   `capturing` after their window plus slack, or `queued` for 10 minutes, are
   failed; all captures expire after 7 days.
6. apid parses, normalizes (CPU nanoseconds or live heap bytes; customer
   labels dropped) and merges a kind across processes for the
   `/view` (function table and call tree) and `/pprof` (download) endpoints.

Only apid parses pprof. vmmd bounds and relays the JSON envelope; schedd
stores bytes.

### Heap semantics

| Runtime | Collector | A capture reports |
|---|---|---|
| Go | `runtime/pprof` heap profile | the sampled live heap of the process |
| Node | V8 sampling heap profiler (`@datadog/pprof`, already pinned by the Pyroscope SDK) | allocations made during the window that are still live at its end |
| Python | `tracemalloc` snapshot encoded as pprof | allocations made during the window that are still live at its end |

"Allocated during the window and still live" is the signature of a leak.
Node `Buffer` contents live outside the V8 heap and appear as `(external)`.
`tracemalloc` costs Python throughput while tracing, so it runs only while a
heap capture or continuous heap collection is active.

### Continuous heap profiles

`profiling.kinds: [cpu, heap]` adds heap snapshots once per window to
continuous collection. profiled stores them as the Pyroscope series
`memory:inuse_space:bytes:space:bytes` without route frames or coverage
metadata. `GET /v1/apps/{slug}/profiles/heap` narrows the query to the last
collection window before `end`, since merged snapshots add up.

## Consequences

- Existing deployments gain dormant collectors on their next deploy after
  the operator flag is set.
- A capture briefly runs profilers in a serving instance; CPU sampling costs
  match ADR-819, heap sampling costs are runtime-specific as above.
- A capture interrupted by a park, migration or schedd restart fails with a
  reason rather than returning partial data.
- Custom images still need an instrumented runtime; their captures finish
  `ready` with zero processes and an explanatory reason.

## Rejected alternatives

- **apid → schedd gRPC.** Violates the ownership rule (apid records intent in
  Postgres); the queue also survives restarts.
- **Waking parked apps to profile them.** Profiles a different process than
  the one under investigation and costs a wake per click.
- **Parsing in vmmd or guest-init.** vmmd is the only root component; guest
  merging would pull pprof into PID 1 for no benefit.
- **Full heap snapshots (`.heapsnapshot`).** Size scales with the heap and
  pauses the process; deferred until object-storage transfer exists for
  multi-megabyte guest artifacts.
