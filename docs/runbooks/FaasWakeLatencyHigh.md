# FaasWakeLatencyHigh

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `gateway_wake_latency_seconds_bucket` (gatewayd `/metrics`).
Spec: §12 (gateway_wake_latency_seconds p95 ≤ 0.8 s target, > 1.5 s warn).
Severity: warn.

## Symptom

End-to-end cold-wake latency has crossed 1.5 s at p95 over a 5-minute
window. The §13 hard limit is 350 ms; the alert threshold (1.5 s) is
looser because the §13 budget is per-customer p50, not fleet p95.

The most likely cause is **snapshot rot** (ADR-005) — when Firecracker
upgrades, snapshots go stale and wakes fall back to cold-boot. Cross-check
`FaasColdBootFallbackHigh` to confirm.

## Verify

```bash
curl -fsS http://127.0.0.1:9090/metrics | grep gateway_wake_latency_seconds
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=histogram_quantile(0.95,sum(rate(gateway_wake_latency_seconds_bucket[5m]))by(le))'
```

## Check

```bash
journalctl -u gatewayd schedd vmmd --since '-15m' --no-pager | grep -iE 'cold.boot|fallback|snapshot'
fcvm-snapshot --list 2>/dev/null | head -20  # if fcvm-cli exists
```

If the cold-boot fallback rate is also elevated, follow the
`FaasColdBootFallbackHigh` runbook. Otherwise the cause is most
likely host-level contention: check `iostat`, `vmstat`, and the
cgroup memory.current of the faas-cp.slice.

## Silence

```bash
amtool silence add \
  --matchers='alertname=FaasWakeLatencyHigh' \
  --duration=30m \
  --comment='snapshot rotation in progress'
```

## Recover

If snapshot rot is the cause, snapshot lazy re-creation (§4.6 + ADR-005)
heals on its own as cold-boot completes. If host contention, the
§13 limits (6 GB cp, 47.6 GB tenant) become the binding constraint;
drain the cp slice or evict a tenant to recover.
