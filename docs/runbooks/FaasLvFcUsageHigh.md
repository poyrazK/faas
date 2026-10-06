# FaasLvFcUsageHighWarn / FaasLvFcUsageHighPage

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metric: `fcvm_lv_fc_used_pct` (schedd `/metrics/fcvm`): how full the
filesystem mounted at `/srv/fc` is, read with statfs, so it works on LVM,
partitions and bare cloud disks alike. Before the statfs probe the gauge
read `lvs -o data_percent`, which is empty for non-thin volumes and absent
on hosts without LVM, and stayed at 0. The same reading drives imaged's
budget-pressure snapshot eviction.
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
curl -fsS http://<schedd-metrics-addr>:9103/metrics/fcvm | grep fcvm_lv_fc_used_pct
df -h /srv/fc
journalctl -u faas-imaged --since '-1h' --no-pager | grep 'gc tick' | tail -1
```

`lv_fc_pct_known=false` in the gc tick means imaged cannot read the
volume, so budget-pressure eviction is off until it can; check that
`FAAS_STORAGE_ROOT` (default `/srv/fc`) exists on that host.

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

Grow the volume, then the filesystem: extend the LV (`lvextend`) on LVM
hosts, or resize the provider disk on cloud hosts, then
`xfs_growfs /srv/fc`. The gauge re-reads the filesystem within one scrape;
no restart is needed. The fleet snapshot fleet average
(`fcvm_snapshot_fleet_avg_bytes`) typically improves 5-10% after a
resize as orphaned snapshots get GC'd by imaged's reclaim loop.
