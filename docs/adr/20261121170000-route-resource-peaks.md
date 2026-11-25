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
inherits variables, secrets and bindings; CPU-heavy routes are split too.

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
returns. The body names the route, method and the suggestion's
`suggestion_digest`; apid recomputes the suggestion and refuses with 409
`route_split_changed` when it no longer matches, so nobody applies a split
they did not see. One split per app runs at a time.

The split is a durable row (`route_splits`, CHECKed phase) that apid's
`pkg/apid/routesplit` worker advances idempotently. apid is the only writer
of apps, deployments and edge rules, so no other owner is bypassed:

1. `target_created`: a standalone app in the source's account, org and
   project, named `<slug>-<last static path segment>` (a taken name gets
   the deploy conflict suffix), sized `route_ram_mb` (CPU splits: the
   source's RAM), with the source's CPU, idle timeout and authn policy.
   It inherits the source's plain variables and secret references, and its
   sealed secret rows by ciphertext: secrets are sealed to the host
   identity, not to the app, so apid copies them without opening them.
2. `target_deploying` → `target_live`: one deployment of the source's live
   artifact (the environment clone's artifact copy: same image, rootfs
   layer and overrides, no rebuild), reason `route-split:<id>`. Only the
   request workload is copied: never crons, workers, queue consumers,
   event subscriptions or schedules, which would run twice.
3. `rule_live`: a `kind=route` edge rule on each of the source's hosts for
   the method and path glob, pointing at the target, created enabled only
   after the target is live.
4. `main_resized` (memory splits): the source app is resized to
   `main_ram_mb` through the same path as `app scale --plan`.
5. `done`.

A failure before step 3 rolls back: the target app is deleted and the row
ends `rolled_back` with the error. After step 3 the split is serving, so a
failed resize ends `done` with a warning and the resize command to retry.
`DELETE /v1/apps/{slug}/route-splits/{id}` undoes a finished split in
reverse: rule off, source size restored, target deleted.

Bindings ship in a second slice. Until then a source app with managed
bindings (Postgres, Redis, buckets, queues, service bindings) is refused up
front with `route_split_bindings_unsupported` and the binding list, so no
split ever serves a route without the data it needs. The second slice
copies request-path bindings with the environment clone's binding
definitions and still never copies consumers.

`gregale app scale <slug> --suggest --apply` posts the split and follows
its phases; `gregale app scale <slug> --split-undo <id>` reverts it.
