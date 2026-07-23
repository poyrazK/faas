# FaasBuildQueueBacklog

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `builderd_build_queue_wait_seconds_bucket` (builderd `/metrics`).
Spec: §12 (build_queue_wait_p95 < 60 s target, > 300 s warn).
Severity: warn (no page tier per spec; builds are not customer-blocking).

## Symptom

Builds are queueing > 300 s at p95 over a 5-minute window. The 2nd
opportunistic builder slot (ADR-003 — runs only when tenant residency
< 60%) is the most common cause: tenant wakes filled the box, the
2nd slot got revoked, and now the 1 guaranteed slot can't drain.

## Verify

```bash
curl -fsS http://127.0.0.1:9105/metrics | grep builderd_build_queue_wait
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=histogram_quantile(0.95,sum(rate(builderd_build_queue_wait_seconds_bucket[5m]))by(le))'
```

## Check

```bash
systemctl status builderd
journalctl -u builderd --since '-15m' --no-pager | grep -iE 'slot|admit|reject'
cat /sys/fs/cgroup/faas-build.slice/memory.current
cat /sys/fs/cgroup/faas-tenant.slice/memory.current
```

If tenant residency is > 60%, the 2nd slot is intentionally offline —
the build queue is operating correctly under that constraint, and the
"fix" is customer traffic draining, not operator intervention.

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
