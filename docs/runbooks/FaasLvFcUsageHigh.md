# FaasLvFcUsageHighWarn / FaasLvFcUsageHighPage

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `fcvm_lv_fc_used_pct` (schedd `/metrics/fcvm`).
Spec: §12 (lv_fc_used_pct > 80 warn, > 90 page).

## Symptom

The lv-fc logical volume is past 80% / 90% used.

- Warn tier (`FaasLvFcUsageHighWarn`) trips at > 80% for 10 m.
- Page tier (`FaasLvFcUsageHighPage`) trips at > 90% for 5 m.

§8 says imaged refuses deploys at > 90% and pages at 80%; §12 says
warn at 80, page at 90. The alert uses §12 verbatim (the more lenient
set) — surfacing the §8 / §12 contradiction as a follow-up spec drift
issue, not a rule-level decision.

## Verify

```bash
curl -fsS http://127.0.0.1:9103/metrics/fcvm | grep fcvm_lv_fc_used_pct
lvs /dev/vg0/lv-fc 2>/dev/null || pvs; df -h /srv/fc
```

## Check

```bash
journalctl -u faas-imaged --since '-15m' --no-pager | grep -iE 'refus|lv-fc|deploy'
```

A failing deploy from imaged at > 90% is the expected behaviour; the
alert is the operator's leading indicator to resize lv-fc before the
next deploy wave fails.

## Silence

```bash
amtool silence add \
  --matchers='alertname=~"FaasLvFcUsageHigh.*"' \
  --duration=2h \
  --comment='lv-fc resize scheduled'
```

## Recover

Resizing lv-fc requires the LV-resize playbook (in `docs/ops/`).
Briefly: extend the LV, xfs_growfs the filesystem, restart imaged
so the gauge re-reads the new size. The fleet snapshot fleet average
(`fcvm_snapshot_fleet_avg_bytes`) typically improves 5-10% after a
resize as orphaned snapshots get GC'd by imaged's reclaim loop.
