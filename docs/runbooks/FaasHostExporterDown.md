# FaasHostExporterDown

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml` (group `faas_host_resources`).
Metric: `up{job=~"node|node-compute"}`. Severity: page.

## Symptom

Prometheus has failed to scrape a host's node_exporter for 5 minutes.
Every host disk, memory and clock alert for that host is blind until it
recovers, which is why this pages.

On the control plane node_exporter listens on `127.0.0.1:9100`
(`job="node"`). Compute hosts are discovered through the active-node
HTTP-SD endpoint (`job="node-compute"`), so a compute host that is down,
unreachable over the private network, or deregistered shows up here too.

## Check

On the affected host:

```bash
systemctl status node_exporter --no-pager
journalctl -u node_exporter --since '-30m' --no-pager | tail -20
curl -fsS http://127.0.0.1:9100/metrics | head -3
```

From the control plane, for a compute host:

```bash
curl -fsS --max-time 5 http://<instance>/metrics | head -3
```

If the host itself is unreachable, check the provider console,
`FaasDaemonDown` for the same host, and
[FaasComputeNodeStuckInactive](FaasComputeNodeStuckInactive.md).

## Recover

- Service stopped or crashed: `sudo systemctl restart node_exporter`.
- Binary or unit missing: re-run the `node_exporter` role for that host.
- Network path broken (compute only): check nftables on the compute host
  allows the control plane to reach port 9100, and that the private DNS
  name in the target resolves.

## Silence

```bash
amtool --alertmanager.url=http://127.0.0.1:9094 silence add \
  alertname=FaasHostExporterDown instance="<instance>" \
  --duration=1h --comment='node_exporter repair in progress'
```
