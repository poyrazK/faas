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
- Native acceptance (`TestRouteResourcePeaksMetal`, lane
  `route-resource-peaks-only`) passed on a nested-KVM host: `/grow` held all
  memory peaks at 25% presence. Its first run found that a single-process
  app's cgroup has no `memory.max`/`cpu.max` of its own; the guest now
  reports the VM's `MemTotal` and vCPUs as the limit in that case.

## Amendment 1 (2026-11-22): split suggestion

The read route adds `split` when one route holds at least 80% of at least
three attributed memory peaks while running in at most 30% of active
minutes, and the rest of the app would fit in less memory than it has now
(`pkg/routeresources.SuggestSplit`). Sizes add 25% headroom and round up to
64 MB:

- the route's app: the route's p95 peak;
- the main app: the median peak plus the p95 rise in minutes without the
  route. Not the p95 peak without it: apps keep the memory a route raised
  for minutes afterwards (the acceptance run's quiet minutes after `/grow`
  stayed at 272 MB), which would size the main app for the route it no
  longer serves.

`route_ram_over_plan` marks a route that needs more than the plan's largest
app. `gregale app scale <slug> --suggest` (hidden with the capability)
prints the split as four commands: deploy the same code under a second
name, size it, add the `kind=route` edge rule for the route's path glob,
then size the main app down. Nothing is applied for the customer; every
resize goes through `app scale --plan`.

## Amendment 2 (2026-11-26): apply the split; CPU splits

Owner decisions (2026-11-26): the split runs server-side; the route's app
inherits variables, secrets and bindings (bindings in a second slice, below);
CPU-heavy routes are split too.

### CPU splits

`SuggestSplit` keeps the memory rule first. When no route qualifies on
memory, a route that holds at least 80% of at least three CPU peaks while
running in at most 30% of active minutes is suggested as a CPU split
(`kind: "cpu"`). Billing is plan RAM per second (§4.7), so a CPU split
saves no money: it isolates the route so its bursts stop starving the
app's other routes. Both apps keep the current size; the route's app can be
resized later. `kind: "memory"` splits keep amendment 1's sizing.

### Applying a split

`POST /v1/apps/{slug}/route-splits` applies the suggestion the read route
returns. The body repeats the suggestion (kind, route, method, both sizes,
window); apid recomputes it over the same window and refuses with 409
`route_split_changed` when it no longer matches, so nobody applies a split
they did not see. One split per app is in flight at a time (409
`route_split_in_flight`, a partial unique index). Splitting takes the
strictest permission of what it does (owner or admin: it writes edge rules
and may delete the app it made).

The split is a durable row (`route_splits`, CHECKed phase) that apid's
`pkg/apid/routesplit` worker advances, every step fenced by a
compare-and-set on the phase so replicas and restarts are safe. A pass first
claims the row (`lease_until`, two minutes, released when the pass ends), so
two apid replicas never run one split's side effects at once. Every step
acts as the source app's resource account under a grant for the one app it
addresses, whoever asked. apid is the
only writer of apps, deployments and edge rules, and it acts through its
own create-app, scale, delete-app and edge-rule handlers in process (as the
dashboard does), so the route's app gets the same validation, quotas,
budget charge, audit and notifications as one a customer makes:

1. `creating`: the row reserves the target name, `<slug>-<last static path
   segment>` (a random five-character suffix when taken). apid creates a
   standalone app in the source's account and org, sized `route_ram_mb`
   (CPU splits: the source's RAM). When its deployment is seeded it takes
   every runtime and security setting of the source (the environment
   promotion's settings set: egress allowlist and ports, auth, IP
   allowlist, rate limits, security policy, declared routes, CORS, start
   command), so the route runs under the rules it ran under before. It
   keeps its own RAM, scales to zero (no min instances) and gets no work
   policies or queue bindings.
2. `deploying`: in the same request apid creates one deployment of the
   source's live artifact (the environment branch's artifact copy: same
   image, rootfs layer, sidecar layers and overrides, no rebuild), reason
   `route-split:<id>`, and copies the source's default-scope variables and
   non-managed secret rows by ciphertext (secrets are sealed to the host
   identity, not to the app). Only the request workload is copied: never
   the release command, workflows, crons, workers, queue consumers, event
   subscriptions or schedules, which would run twice.
3. `rule_live`: once the deployment is live, a `kind=route` edge rule on
   each of the source's hosts (its platform host and verified app-wide
   custom domains) for the method and path glob, pointing at the target.
   The rules are named `route-split:<id>` so a retried pass reuses them and
   undo finds them.
4. `done`: a memory split sizes the source to `main_ram_mb` first.

A failure before the rules are live rolls back: the rules made so far go
first (so the path never routes to a deleted app), then the target app,
and the row ends `rolled_back` with the error. Once recorded, a split's
creation runs to the end even if the caller disconnects. The worker pages
through every in-flight split each pass, so waiting splits never hold
newer ones back, and two undo requests for one split never both act.
A deployment that is not live after 15 minutes, or a row left in
`creating` for 5 minutes, rolls back the same way. A rollback that cannot
delete the route's app within an hour ends `rolled_back` naming the app for
the customer to delete, so the source can be split again. After the rules
are live the split is serving: a transient resize failure (5xx, 429, 409)
is retried for 15 minutes, and a refusal or one that outlasts the window
ends `done` with a warning. Undo counts a 404 from deleting the route's app
as deleted only when the app is really gone.
`DELETE /v1/apps/{slug}/route-splits/{id}` undoes a finished split: the
source gets its previous size back first (it serves the route again once
the rules go), then the rules and the target app are deleted. A failed step
leaves the split `done`, so undo can be retried.

The first slice splits standalone apps only. Apps in a project (their
workloads are source-managed), previews, and apps with managed bindings
(Postgres, object storage) or service bindings are refused up front with
422 `route_split_unsupported` and what blocks them, so no split ever serves
a route without the data it needs. So are apps with their own edge rules
(the route's app answers on its own hostname, where those deny, auth, IP
and rate-limit rules would not run) and apps with a static egress IP
(unique to one app). A second slice copies request-path
bindings with the environment clone's binding definitions and still never
copies consumers.

`gregale app scale <slug> --suggest --apply-split` applies the suggestion it
read, `--split-status` lists the app's splits and `--undo-split <id>`
reverses one. (`--apply` already applies a saved scale plan.)
