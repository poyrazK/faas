# FaasHighResidentRam / FaasHighResidentRamWarn

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `fcvm_resident_ram_pct` (emitted by schedd at `/metrics/fcvm`).
Spec: §12 (resident_ram_pct ≤ 100 target, > 80 warn, > 92 page).

## Symptom

The sum of `(ram_mb + 8)` across live instances, expressed as a percent
of the 47,600 MB tenant budget, has crossed the §12 threshold.

- Warn tier (`FaasHighResidentRamWarn`) trips at > 80% for 10 m.
- Page tier (`FaasHighResidentRam`) trips at > 92% for 5 m.

## Verify

```bash
curl -fsS http://127.0.0.1:9103/metrics/fcvm | grep fcvm_resident_ram_pct
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=fcvm_resident_ram_pct'
```

`fcvm_resident_ram_pct` should track the per-app instance RAM + 8 MB
overhead. A persistent 90%+ reading under no load is a gauge wiring bug.

## Check

```bash
systemctl status schedd vmmd
journalctl -u schedd -u vmmd --since '-15m' --no-pager | grep -E 'admit|reject'
```

The most common cause is a single Scale-tier app pinned at max
concurrency; the second most common is the watchdog failing to park
parked_at=now apps because their instance rows leaked past the cleanup
sweep.

## Silence

```bash
amtool silence add \
  --matchers='alertname=~"FaasHighResidentRam.*"' \
  --duration=30m \
  --comment='investigating tenant eviction'
```

`amtool` ships with the alertmanager role; reachable from the box at
`/usr/local/bin/amtool --alertmanager.url=http://127.0.0.1:9093`.

## Recover

The invariant §6.2/2 (Σ(ram_mb+8) ≤ 47,600 MB) is hard-enforced; the
alert is the operator's leading indicator that a wake will start
failing admission. Eviction policy is per-tenant — see `docs/ops/`
for the eviction runbook.
