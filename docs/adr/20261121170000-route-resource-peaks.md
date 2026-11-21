# ADR-20261121170000: Route resource peaks

- Status: Proposed (dark behind the `route_resource_peaks` platform flag;
  the owner decides which accounts to release it to)
- Date: 2026-11-21
- Related: ADR-127 (request telemetry), ADR-20261103113952 (analytics
  rollups), ADR-121 (workload OOM), ADR-958 (in-guest telemetry bridge)

## Context

An app runs at one size. In real apps most routes need little memory, but
one route (an export, a PDF render, an image resize) needs several times as
much, and the whole app is sized for it: 98% of requests could run at
256 MB while one `/export` keeps every instance at 1–2 GB. The fix is to move
that path to its own app (a `kind=route` edge rule to a larger app, or
`kind=async` when the client can wait) and size the main app down. Customers
cannot see which route it is.

Gregale has no per-request memory measurement for HTTP apps. Requests reach
the app over the network, not through a guest-side proxy, so nothing inside
the VM sees request boundaries. The only per-request resource numbers are
the function runners' `X-Faas-Guest-Peak-Rss-Mb`, which cover functions only
and measure the whole process. vmmd's per-VM `memory.current` is the
Firecracker process: guest memory, once touched, never comes back to the
host, so after warm-up it no longer shows peaks. Request telemetry rows are
minute buckets whose `instance_id` is dropped as soon as replicas mix, and
they are capped per account, so they cannot be joined to an instance's
memory either.

## Decision

Attribute peaks by correlation over instance-minutes, from two signals that
share the key (app, instance, minute):

1. **Resources, from the guest.** guest-init reads the app cgroup's
   `memory.current` and `cpu.stat` every second and sends a ten-second slot
   (start, peak and end memory, CPU used and the busiest second's rate, the
   cgroup limits, and how long ago the peak was) as guest event type `0x0C`.
   vmmd stamps each slot with its own clock (receive time minus the peak's
   age), so guest clock skew after a restore cannot move a peak, and folds
   slots into wall-clock minutes per instance. Closed minutes go to apid's
   new `SpansWriter.IngestInstanceResourceMinutes`, which checks the instance
   belongs to the calling node, and are kept in `instance_resource_minutes`.
2. **Routes, from the gateway.** For apps the flag releases, the gateway
   counts each finished request into every minute it was in flight on its
   instance (requests in the minute it ended, in-flight milliseconds in each,
   at most the last 15 minutes of a long stream, at most 64 routes per
   instance-minute before `__other__`), and sends closed minutes to the new
   `RequestTelemetry.AddInstanceRouteMinutes`. They are kept in
   `instance_route_minutes`.

`GET /v1/apps/{slug}/analytics/route-resources?since=24h` joins the two
(`pkg/routeresources.Analyze`). A minute is a memory peak when memory rose
above where the minute started by at least max(16 MB, 25% of the window's
largest rise): a rise, not a level, because page cache and garbage-collected
heaps keep memory high after the route that raised it. A CPU peak is a minute
whose busiest second reached 80% of the CPU limit. For every route the
response gives the share of attributed peaks it was in flight for, how
often it ran at all (`presence_share`), the peaks it was the only route for,
and the p95 peak and rise in minutes with and without it.
`mem_peak_p95_without_mb` approximates what the app would need without the
route. Rows that served requests with no samples (an older guest, a lost
frame) are counted in `route_minutes_without_resources` and left out of
every share.

Delivery is best-effort on both sides: a batch is sent once and dropped on
failure, never resent, so a lost answer never counts twice; buffers are
bounded and drops are counted (`gateway_instance_route_minutes_dropped_total`).
When apid answers that an app is not released, vmmd stops forwarding that
app for five minutes. meterd sweeps both tables after 14 days.

The `route_resource_peaks` platform flag (capability `route-resource-peaks`,
maturity `internal`) gates the gateway counting, apid's writes and the read
route. `FAAS_ROUTE_RESOURCE_PEAKS=0` stops a node's vmmd or gateway from
sending anything.

## Consequences

- Attribution is a correlation, not a measurement. A route present in every
  minute shares every peak; `presence_share` next to `high_memory_share` is
  what separates the heavy route from the busy one, and
  `sole_high_memory_minutes` is the strongest evidence. Peaks shorter than
  the one-second sample interval are missed.
- guest-init reads two cgroup files a second and opens one vsock stream per
  ten seconds while the app does any work (idle slots are not sent). Quiet-
  paused VMs send nothing.
- Old guests send no slots, so their apps show only
  `route_minutes_without_resources` until redeployed on a new base.
- Follow-up (not in this change): a `gregale app scale --plan` suggestion
  that, when one route holds most peaks while running in few minutes,
  prints the split (a new app at the route's size, a `kind=route` rule, and
  the main app sized to `mem_peak_p95_without_mb`).
