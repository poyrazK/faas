# FaasComputeMetricsDiscoveryDegraded

## Meaning

The control-plane Prometheus HTTP service-discovery producer is stale, is
returning fewer compute targets than the active registry expects, or is
returning healthy targets that Prometheus cannot scrape, or has multiple
scraped producers for the same discovery gauge. Prometheus itself may
still be up while remote `gatewayd-internal`, vmmd, imaged, builderd or
Promtail metrics are frozen, incomplete, or absent.

The customer-facing app routes and automatic OpenAPI surfaces read the
fleet-wide `gateway_requests_by_route_total` aggregate from this Prometheus
instance. They report `source=partial` while some expected
`gatewayd-internal` targets are down and `source=unavailable` when no expected
collector is healthy or the query fails. A healthy fleet with no route series
is a valid no-traffic state (`source=live`, `routes=[]`).

The producer is apid's loopback-only endpoint. It reads active
`compute_nodes` rows with a configured `gateway_target_url`; it does not read
Ansible inventory. A successful empty response is valid when there are no
scrape-ready compute nodes, so the empty-target alert compares the target count
with the producer's registry-node count.

## Verify

Query the producer-side signals in Prometheus:

```text
apid_metrics_discovery_registry_nodes{job="apid",exported_job="gatewayd-internal"}
apid_metrics_discovery_targets{job="apid",exported_job="gatewayd-internal"}
apid_metrics_discovery_invalid_targets{job="apid",exported_job="gatewayd-internal"}
time() - apid_metrics_discovery_last_success_timestamp_seconds{job="apid",exported_job="gatewayd-internal"}
rate(apid_metrics_discovery_requests_total{job="apid",exported_job="gatewayd-internal",outcome=~"error|unavailable"}[10m])
```

The default Prometheus scrape uses `honor_labels=false`: apid's own `job`
label is renamed to `exported_job`, while `job` identifies the apid scrape.
Keep `job="apid"` and repeat with the affected compute job in `exported_job`:
`gatewayd-internal`, `vmmd`, `imaged`, `builderd`, `node-compute`, `realtimed`,
`schedd-compute`, or `promtail-compute`. Do not change scrape label precedence
or enable `honor_labels` to repair these queries.
The expected state for an enabled discovery job is:

- a nonzero `last_success` timestamp within the past 120 seconds;
- `targets` equal to `registry_nodes` for each job; and
- `invalid_targets` equal to zero.

apid preinitializes zero gauges for optional and single-box-unused discovery
jobs. A zero refresh timestamp with no observed discovery request is unproven
collection intent and does not raise a bootstrap-staleness warning. Once a
request is observed (including failed requests), a persistently zero refresh
warns. Previously successful snapshots still warn when their refresh stops.
A successful empty fleet has a fresh nonzero refresh timestamp, so it remains
distinct from these unrequested initial zeros. Request counters cover the current
apid process lifetime, including manual loopback diagnostics; they are not an
inventory of currently enabled scrape jobs.

The rules normalize all four gauges into job-only recordings before joining
with compute `up` series:

```text
faas_compute_metrics_discovery_registry_nodes:by_job{job="gatewayd-internal"}
faas_compute_metrics_discovery_targets:by_job{job="gatewayd-internal"}
faas_compute_metrics_discovery_invalid_targets:by_job{job="gatewayd-internal"}
time() - faas_compute_metrics_discovery_last_success_timestamp_seconds:by_job{job="gatewayd-internal"}
```

Each gauge must have exactly one apid source per intended compute job. A
missing normalized series means unavailable evidence, not zero targets or a
healthy empty fleet. `FaasComputeMetricsDiscoveryAmbiguous` warns if any gauge
has multiple sources; they are neither summed nor arbitrarily chosen. Inspect
raw series by `instance` for duplicate apid scrape targets or extra producer
labels. This is an explicit singleton-source contract, not HA snapshot merging.
An entirely missing source does not establish an expected deployment role:
check apid's `up`, raw metrics, and `gregalectl obs input-health --expected-role
control-plane-v1 --json` rather than treating absent alerts as healthy evidence.

Check the downstream coverage signals next. These compare the latest healthy
target list from apid with the `up` series Prometheus is actually producing:

```text
faas_compute_metrics_healthy_targets:by_job
faas_compute_metrics_scrape_coverage:by_job
up{job=~"gatewayd-internal|promtail-compute"}
max by (job, node) (scrape_duration_seconds{job=~"gatewayd-internal|promtail-compute"})
```

The expected state is one healthy target per discovered target and coverage of
`1` when at least one target is expected. A fresh zero-node registry has
`targets=registry_nodes=invalid_targets=0`, coverage `0` (the denominator is
clamped to one), and no empty/low-coverage warning. Zero and future refresh
timestamps cannot enable downstream scrape warnings; a persistently invalid
refresh raises the stale-discovery warning. A target with `up == 0` is a downstream scrape
failure. If coverage is below `1` but no `up == 0` series exists, all or part
of the target list disappeared before Prometheus created a scrape series.
The rendered control-plane jobs have a hard `target_limit` of 1,000, and apid
rejects a larger configured registry snapshot with an explicit discovery
failure. This prevents a fleet expansion from silently creating an unbounded
scrape set.

For an affected app, compare the exact inputs used by the fleet route reader:

```text
max by (node_id) (up{job="gatewayd-internal"})
max by (node_id, route) (gateway_requests_by_route_total{app="<app-id>"})
```

Then call `gregale app <slug> routes --json`. The response's
`collectors_healthy` must match the healthy expected node IDs and
`collectors_expected` must match the active, scrape-ready compute registry.
The same provenance is embedded in automatic OpenAPI documents under
`x-faas-observed-routes`, so it survives an apid restart and remains visible
to `gregale inspect`.

Check the Prometheus target view for the node-level error:

```sh
curl -fsS http://127.0.0.1:9095/api/v1/targets \
  | jq '.data.activeTargets[] | select(.labels.job == "gatewayd-internal" or .labels.job == "promtail-compute") | {job: .labels.job, instance: .labels.instance, health: .health, error: .lastError}'
```

On the control-plane host, verify that apid's internal endpoint is reachable
from the Prometheus user. Query only endpoints for enabled HTTP-SD jobs:
manual diagnostics also count as discovery attempts. The endpoints are
loopback-only:

```sh
curl -fsS http://127.0.0.1:9101/v1/internal/metrics/targets
curl -fsS http://127.0.0.1:9101/v1/internal/metrics/vmmd-targets
curl -fsS http://127.0.0.1:9101/v1/internal/metrics/imaged-targets
curl -fsS http://127.0.0.1:9101/v1/internal/metrics/builderd-targets
curl -fsS http://127.0.0.1:9101/v1/internal/metrics/promtail-targets
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

For ambiguous discovery, reconcile duplicate apid scrape targets through the
managed Prometheus configuration. Verify that each raw gauge has one producer
for the affected `exported_job` and that its normalized recordings return.
Do not sum snapshots or select a convenient producer to silence the warning;
intentional HA needs an explicit source-authority design.

Fix the registry row through the compute-node registration/reconciliation
path. Do not hand-edit `prometheus.yml` or add provider IPs as a workaround;
the next HTTP-SD refresh should replace the target automatically.

New or updated compute-node registrations reject loopback and wildcard
`gateway_target_url` values up front. If registration returns
`Invalid gateway_target_url`, provide the node's stable private hostname or
address reachable from the control plane on port 8080; do not retry with a
loopback alias such as `localhost`.

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
