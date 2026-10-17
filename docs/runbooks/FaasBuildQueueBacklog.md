# Build queue backlog and observation health

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `builderd_build_queue_wait_seconds_bucket` (builderd `/metrics`),
`faas_build_queue_depth`, `apid_build_queue_collection_success`, and
`apid_build_queue_collection_last_success_timestamp_seconds` (apid `/metrics`).
Spec: §12 (build_queue_wait_p95 < 60 s target, > 300 s warn).
Severity: warn (no page tier per spec; builds are not customer-blocking).

## Symptom

`FaasBuildQueueBacklog` reports builds queueing > 300 s at p95 over a
5-minute window. `FaasBuildQueueDepthHigh` reports more than 50 queued builds
for ten minutes, using only complete, fresh queue observations.
`FaasBuildQueueTelemetryUnavailable` reports an apid that scrapes successfully
but has no complete fresh queue observation for five minutes.

Each compute
node admits one builder because two ordinary builders can exceed the 5 GiB
parent cgroup. The queue grows when arrival rate exceeds that safe drain rate.

## Verify

```bash
curl -fsS http://127.0.0.1:9105/metrics | grep builderd_build_queue_wait
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=histogram_quantile(0.95,sum(rate(builderd_build_queue_wait_seconds_bucket[5m]))by(le))'
```

In Prometheus, inspect each apid independently:

```promql
faas_build_queue_depth{job="apid"}
apid_build_queue_collection_success{job="apid"}
time() - apid_build_queue_collection_last_success_timestamp_seconds{job="apid"}
faas_build_queue_observation_healthy
up{job="apid"}
```

The queued count excludes running and terminal builds. Each apid reads the
same fleet total every thirty seconds with a three-second deadline. Never sum
these counts across apids: two counts of 30 are two observations of the same
queue, not 60 builds. Compare differing snapshots using their observation times.
The queued-only `builds_queued_count_idx` index supports this periodic count;
confirm the migration is applied when investigating slow reads on a large
build history.

A successful zero is an observed empty queue. Depth is omitted before the first
successful read, immediately after a failed read, and once its successful read
is older than 120 seconds or in the future. The last-success timestamp freezes
on failure. A success gauge of one alone is insufficient: a stopped poller can
leave its last read marked successful. The healthy recording requires the depth,
success, fresh nonzero timestamp, and successful scrape from the same instance.

Missing depth does not mean the queue drained. The backlog alert resets when
its evidence becomes unavailable; use the separate telemetry warning to track
that gap. A healthy sibling cannot clear a failing collector's warning. A
failed apid scrape is covered by daemon-down monitoring instead. These rules
cannot discover an apid scrape omitted from Prometheus configuration.

For the telemetry warning, check `journalctl -u apid --since '-15m' --no-pager`
for `build queue observation failed`, Postgres availability/pool pressure, and
whether the running apid binary includes the collector. An expired timestamp
with successful scrapes suggests a stopped poller; a future timestamp suggests
clock inconsistency. The existing admin builder-heartbeats endpoint provides an
independent on-demand count when its database read succeeds.

## Check

```bash
systemctl status builderd
journalctl -u builderd --since '-15m' --no-pager | grep -iE 'slot|admit|reject'
cat /sys/fs/cgroup/faas.slice/faas-cp.slice/faas-cp-build.slice/memory.current
cat /sys/fs/cgroup/faas-tenant.slice/memory.current
```

If the sole slot is occupied by a healthy build, the queue is operating
within its memory fence. Add an eligible compute node when sustained build
arrival rate exceeds one slot per node.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasBuildQueueBacklog' \
  --duration=1h \
  --comment='tenant traffic expected to drain'
```

## Recover

For telemetry failure, restore database access or the collector and confirm a
new successful observation, advancing timestamps, and healthy recording for each
apid. An alert clearing because inputs disappeared is not backlog recovery.
For backlog, confirm fresh counts decline and queue wait improves. The builder
execution timeout does not bound how long a queued build waits; queued depth
alone does not establish slot-admission errors or builder liveness.
