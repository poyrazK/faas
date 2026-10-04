# FaasHostMemoryLow / FaasHostMemoryCritical

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml` (group `faas_host_resources`).
Metrics: `node_memory_MemAvailable_bytes`, `node_memory_MemTotal_bytes`
(node_exporter). Severity: Low warns; Critical pages.

## Symptom

`MemAvailable` has stayed below 10% (warn, 15 minutes) or 5% (page,
5 minutes) of `MemTotal` on a host. The kernel OOM killer runs next; see
[FaasBuilderSliceOOM](FaasBuilderSliceOOM.md) for the `FaasHostOOMKill`
alert that follows an actual kill.

Tenant admission caps guest RAM below the host total, so on a compute host
low memory points at something outside the admission ledger: a leaking
daemon, an oversized build, page tables or a guest that was not accounted.
On the control plane the usual consumers are PostgreSQL, Prometheus and
apid.

## Check

```bash
free -m
ps -eo rss,pid,comm --sort=-rss | head -15
systemd-cgtop -m --depth=2 -n 1
```

Compare per-slice usage with the RAM budget (spec §13):

```bash
for s in faas-cp.slice faas-tenant.slice faas-cp-build.slice; do
  printf '%s ' "$s"; systemctl show "$s" -p MemoryCurrent -p MemoryMax | tr '\n' ' '; echo
done
```

On a compute host, compare live guests with the admission ledger. Compute
daemons bind metrics to the node's manifest name (for example
`fsn-2.gregale.dev`), not loopback, and the provider hostname can differ:

```bash
pgrep -c firecracker
curl -fsS "http://<node-manifest-name>:9103/metrics/fcvm" | grep fcvm_resident_ram_pct
```

## Recover

- A single daemon far above its normal RSS: capture
  `journalctl -u <unit> --since '-1h'` and restart it with
  `systemctl restart <unit>`; open an issue with the RSS growth curve.
- A runaway build: builderd's slice caps builds; if the build slice is at
  its `MemoryMax`, the build is the cause and will be OOM-killed inside
  its own slice.
- Too many guests for the host: park idle apps or drain the node
  (`gregalectl compute-nodes drain --node <fqdn> --reason '<why>'`) and
  investigate why admission let them in.
- On the control plane, Prometheus memory grows with series count; check
  `prometheus_tsdb_head_series` before raising limits.

## Silence

```bash
amtool --alertmanager.url=http://127.0.0.1:9094 silence add \
  'alertname=~"FaasHostMemory.*"' instance="<instance>" \
  --duration=1h --comment='memory investigation in progress'
```
