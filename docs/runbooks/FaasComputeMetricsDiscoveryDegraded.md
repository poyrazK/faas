# FaasComputeMetricsDiscoveryDegraded

## Meaning

The control-plane Prometheus HTTP service-discovery producer is stale or is
returning fewer compute targets than the active registry expects. Prometheus
itself may still be up, but remote `gatewayd-internal` or Promtail metrics can
be frozen, incomplete, or absent.

The producer is apid's loopback-only endpoint. It reads active
`compute_nodes` rows with a configured `gateway_target_url`; it does not read
Ansible inventory. A successful empty response is valid when there are no
scrape-ready compute nodes, so the empty-target alert compares the target count
with the producer's registry-node count.

## Verify

Query the producer-side signals in Prometheus:

```text
apid_metrics_discovery_registry_nodes{job="gatewayd-internal"}
apid_metrics_discovery_targets{job="gatewayd-internal"}
apid_metrics_discovery_invalid_targets{job="gatewayd-internal"}
time() - apid_metrics_discovery_last_success_timestamp_seconds{job="gatewayd-internal"}
rate(apid_metrics_discovery_requests_total{outcome=~"error|unavailable"}[10m])
```

Repeat with `job="promtail-compute"` when the Promtail scrape is affected.
The expected state is:

- `last_success` age below 120 seconds;
- `targets` equal to `registry_nodes` for each job; and
- `invalid_targets` equal to zero.

Check the Prometheus target view next:

```sh
curl -fsS http://127.0.0.1:9095/api/v1/targets \
  | jq '.data.activeTargets[] | select(.labels.job == "gatewayd-internal" or .labels.job == "promtail-compute") | {job: .labels.job, instance: .labels.instance, health: .health, error: .lastError}'
```

On the control-plane host, verify that apid's internal endpoint is reachable
from the Prometheus user. The endpoint is deliberately loopback-only:

```sh
curl -fsS http://127.0.0.1:8081/v1/internal/metrics/targets
curl -fsS http://127.0.0.1:8081/v1/internal/metrics/promtail-targets
```

If either request returns `503`, inspect apid and PostgreSQL:

```sh
journalctl -u faas-apid --since '-15m' --no-pager \
  | grep -iE 'metrics discovery|database|pool|timeout'
systemctl status faas-apid --no-pager
```

For a non-zero `invalid_targets` value, inspect the active rows without
copying credentials or private key material into tickets:

```sql
SELECT id, name, active, gateway_target_url, region, zone
FROM compute_nodes
WHERE active = true
ORDER BY name;
```

## Recovery

Fix the registry row through the compute-node registration/reconciliation
path. Do not hand-edit `prometheus.yml` or add provider IPs as a workaround;
the next HTTP-SD refresh should replace the target automatically.

If the registry is healthy but apid's endpoint remains stale, recover apid's
database connectivity first. Restart apid only after the underlying pool,
network, or migration problem is understood:

```sh
systemctl restart faas-apid
```

Confirm that `last_success` advances and that the target count matches the
active registry before closing the alert. For a target that is present but
unhealthy, continue with the compute-node or Promtail runbook; this alert only
covers discovery production, not the downstream scrape process.
