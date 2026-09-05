# FaasComputeMetricsDiscoveryDegraded

## Meaning

The control-plane Prometheus HTTP service-discovery producer is stale, is
returning fewer compute targets than the active registry expects, or is
returning healthy targets that Prometheus cannot scrape. Prometheus itself may
still be up while remote `gatewayd-internal` or Promtail metrics are frozen,
incomplete, or absent.

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

Check the downstream coverage signals next. These compare the latest healthy
target list from apid with the `up` series Prometheus is actually producing:

```text
faas_compute_metrics_healthy_targets:by_job
faas_compute_metrics_scrape_coverage:by_job
up{job=~"gatewayd-internal|promtail-compute"}
max by (job, node) (scrape_duration_seconds{job=~"gatewayd-internal|promtail-compute"})
```

The expected state is one healthy target per discovered target and coverage of
`1` for each enabled job. A target with `up == 0` is a downstream scrape
failure. If coverage is below `1` but no `up == 0` series exists, all or part
of the target list disappeared before Prometheus created a scrape series.

Check the Prometheus target view for the node-level error:

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

Confirm that `last_success` advances, the target count matches the active
registry, and downstream coverage returns to `1` before closing the alert. For
a target that is present but unhealthy, inspect the node's listener, private
route, firewall, and service readiness before restarting the service. The
`FaasComputeMetricsScrapeTargetDown` alert identifies the affected node; the
`FaasComputeMetricsScrapeCoverageLow` alert covers missing or partial `up`
series.
