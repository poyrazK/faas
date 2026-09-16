# FaasBuildQueueBacklog

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `builderd_build_queue_wait_seconds_bucket` (builderd `/metrics`).
Spec: §12 (build_queue_wait_p95 < 60 s target, > 300 s warn).
Severity: warn (no page tier per spec; builds are not customer-blocking).

## Symptom

Builds are queueing > 300 s at p95 over a 5-minute window. Each compute
node admits one builder because two ordinary builders can exceed the 5 GiB
parent cgroup. The queue grows when arrival rate exceeds that safe drain rate.

## Verify

```bash
curl -fsS http://127.0.0.1:9105/metrics | grep builderd_build_queue_wait
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=histogram_quantile(0.95,sum(rate(builderd_build_queue_wait_seconds_bucket[5m]))by(le))'
```

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

The build queue is bounded by spec §4.6 (10-min build timeout). When
the queue saturates, customers see `409 build slot busy`; the alert
is the operator's leading indicator before the customer impact lands.
