# ADR-743 · Allocate route compute cost by request time

- **Status:** proposed
- **Date:** 2026-10-09
- **Decision:** Request analytics (ADR-242) allocates an app's estimated
  compute value across routes by each route's share of observed request time
  — `SUM(latency_ms × count)` from `request_telemetry` — instead of its share
  of request count. `compute_cost.allocation_method` reports
  `request_time_share`; it reports `request_share` and uses the old split only
  when the window has no request time at all. Routes also expose
  `request_time_ms` and `request_time_share_pct` alongside the existing
  `request_share_pct`.
- **Why:** Billing is plan RAM per running second (§4.7). What keeps an
  instance running is time spent serving, not the number of requests: a
  4-second report endpoint called 10 times holds RAM far longer than a 5 ms
  health check called 900 times, yet request share charged the health check
  90 times more. Customers use per-route cost to decide what to optimise, so
  the estimate must follow the resource actually consumed.
- **Consequences:**
  - The change is one added aggregate column in
    `RequestTelemetryAnalyticsByDimension` (computed per group, including the
    omitted-routes `__other__` bucket) and a new allocation function that
    reuses the existing largest-remainder rounding, so allocations still sum
    exactly to the estimate.
  - Gateway latency includes wake time on cold requests and the full duration
    of streaming responses. Both genuinely hold an instance, so they count.
  - Concurrent requests inside one instance are each counted in full, so time
    share approximates rather than measures RAM-seconds per route. It is still
    an allocation estimate, as before, not invoice math.
  - The split of a route's cost across its deployments stays by request share
    within the route; deployment observations are unchanged.
  - The API change is additive: a new enum value and two new optional fields.
- **Rejected alternatives:**
  - *Guest CPU time.* Only available for supported one-shot runtimes, and CPU
    is not what Gregale bills.
  - *Instance-second attribution per request.* Needs per-instance concurrency
    tracking that request telemetry does not record; time share gets most of
    the accuracy with data that already exists.
