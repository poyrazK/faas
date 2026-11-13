# Notification delivery evidence and local drill

Use [post-deployment monitoring verification](post-deployment-monitoring.md)
to wait for fresh serving and notification evidence after a platform rollout
and retain a release-linked report.

The fleet dashboard shows completed transport requests for the fixed
`faas-page`, `faas-warn` and `faas-heartbeat` receiver groups. It reports collector
freshness, age of the last successful transport observation, failed requests
over ten minutes, and mean completed request duration over ten minutes.
Firing and recovery receipts need separate qualification.

## Read the evidence

Alertmanager 0.27 runs with `--enable-feature=receiver-name-in-metrics`.
The `notification_receiver_metrics` role installs a read-only collector and a
30-second systemd timer on the control plane. It reads the fixed loopback
`http://127.0.0.1:9094/metrics` endpoint without a proxy or redirects. It does
not read receiver configuration, secret files or destination addresses.
It exports to node_exporter's textfile directory. Collection intervals may be
set from 1s through 60s; rule freshness is 120 seconds.

The collector uses `alertmanager_notification_requests_total`, failed request
counts and the completed-request latency histogram. The separate
`alertmanager_notifications_total` counter advances before the retry stage;
subtracting failures from it cannot establish completed success. Latency here
includes failed requests and retry attempts, and excludes grouping delay.

A new successful completed request count must appear in two observations
before the last-success observation clock advances. This avoids confirming a
counter read while a failure counter is still being updated. The timestamp is
the collector's confirmation time, usually delayed by one collection interval;
it is not an exact provider delivery timestamp. A cold start establishes a
baseline and reports **Unknown** until a new success is confirmed. Counter or
process resets rebaseline without inventing a new success; previously confirmed
history is retained. State lives in `/var/lib/faas-notification-receiver-metrics`.

The independent `notification-receiver-inventory.prom` file derives expected
receiver/integration groups from Ansible's configured path presence. It contains
no secret paths or values. Missing transport groups warn even if another group
is healthy. Slack and Discord's Slack-compatible endpoints share the `slack`
integration; multiple destinations in one group are aggregated. Evidence does
not identify which destination accepted an individual alert.

Failed or incoherent collection publishes `collector_up=0` and removes receiver
samples, preserving history on disk. Fresh scrapes of a frozen textfile cannot
establish health: rules check the collector's own run clock and bind counters
to that source. Dashboard queries reject recordings older than two minutes.
Transport success does not establish human acknowledgement or external
watchdog receipt. The fleet dashboard and operator notification-health read
expose receiver groups; the operator read preserves its existing aggregate
fields and adds an independent receiver assessment.

## Operator API and CLI

```bash
gregalectl obs notification-health
gregalectl obs notification-health --json
```

The existing GET `/v1/admin/obs/notification-health` response adds optional
`receiver_health`. Its `collector`, `inventory` and sorted `groups` distinguish
known values from missing evidence. Each group reports its reviewed receiver
role and integration type, whether independent deployment intent expects it,
ten-minute completed request and failed request increases, and the last
successful transport observation. Multiple destinations sharing a group
remain aggregated. No address, secret path, scrape target or provider error
text is returned.

The top-level `state`, `signals`, `integrations` and `limitations` retain their
aggregate meaning. Assess `receiver_health.state` separately. The human output
prints both assessments; for example, aggregate counters can be healthy while
a missing expected receiver makes receiver health degraded. Reads still
require operator authorization, MFA and the operator allowlist and produce
no notification, configuration change or audit write.

Receiver health is **healthy** only when the collector and independent intent
are fresh and each expected group has complete counter increases and a known
successful observation. **Degraded** identifies failed completed requests,
an expected group's missing current telemetry, or a fresh observed group
outside deployment intent. **Partial** preserves insufficient samples, unknown
success history and unavailable or inconsistent evidence. A valid positive
failure remains visible when another signal is unavailable.

A signal's `state=healthy` describes evidence quality. In particular,
`collector.value=0` with healthy read quality means the collector reported a
failure; receiver transport evidence then remains unavailable. Unknown
inventory produces `expected: null`, rather than false. A fresh gauge holding
the collector's cold baseline produces a null `last_success_observation.value`
and human `last_success_observed_at=unknown`. The API never substitutes an
epoch-zero receipt. A known last-success value is Unix seconds of collector
confirmation; its `observed_at` measures the gauge's freshness. An old
confirmed success with a fresh gauge remains historical evidence, not proof
that a future notification will arrive.

The read uses four strict, concurrent Prometheus queries pinned to one time
inside the existing shared three-second deadline. It validates source and
timestamp identity and the collector's run clock, keeps zero and unknown
distinct, and does not reuse cached values. Groups surviving only in retired
counter ranges are excluded. The response is bounded to 32 groups with fixed
receiver roles; unknown names are rejected without returning them.

The new CLI accepts an older server that omits `receiver_health` and prints
`receiver_health=unknown reason=server_does_not_report_receivers`. Both human
and JSON modes validate new receiver metadata before emitting it. Existing
callers keep the aggregate fields, and the generated preview SDKs expose the
new section as optional. A successful read exits normally for partial or
degraded observations; inspect the JSON states for operational decisions.

## Run the local drill

Use the repository's pinned Ansible Python environment (Jinja2 and PyYAML),
the deployment-pinned Prometheus/promtool, and either a pinned Alertmanager
binary or the check script's checksum-verified download:

```bash
NOTIFICATION_DRILL_REPORT=/tmp/notification-drill.json make notification-delivery-check
```

`PROMETHEUS`, `PROMTOOL` and `ALERTMANAGER` can name local binaries. Defaults
are the binaries on PATH; without `ALERTMANAGER`, the wrapper downloads the
version and verifies the SHA-256 pinned by the Alertmanager role. The check
launches isolated processes on random loopback ports with disposable state,
then stops only its own processes. It never contacts deployed services.

The drill reuses the production route tree and watchdog rule. It replaces
provider transports with a local webhook receiver and compresses timing. It
requires matching run IDs and verifies per-alert firing and recovery receipts,
a receiver returning HTTP 503 while the other route delivers, retry recovery,
and per-receiver completed request observations. Finally it stops Prometheus
and Alertmanager while the receiver stays alive and verifies a missed watchdog
deadline. Page and warn test families differ so the production page-over-warn
inhibition does not suppress the warning test.

The JSON report explicitly records `scope=local_webhook_routing_drill`,
`production_providers=not_exercised` and `human_acknowledgement=not_assessed`.
Both deployment-config and nightly CI run the check and retain the report for
seven days. This qualifies local routing, transport observation and receipt
handling; the deployed Slack, email and Pushover adapters and the real external
monitor still require the established on-call procedure.

## Rollout and operational qualification

Use the manual [production qualification workflow](monitoring-qualification.md)
for test-bound firing/recovery receipts and a separate external deadline test.
It retains operator attestations and keeps unverified groups unknown.

Converge the Alertmanager role with its service feature flag, receiver metrics
role and inventory, along with Prometheus rules and Grafana provisioning.
Confirm `faas-notification-receiver-metrics.timer` runs and
`faas_notification_receiver:collector_fresh == 1`. Compare
`faas_notification_receiver:expected` with
`faas_notification_receiver:telemetry_present`; every expected group should
have evidence, even with zero completed requests. Dev mode stops the collector
and removes its textfiles.

Use the [watchdog setup and on-call delivery check](../runbooks/FaasWatchdog.md)
to qualify the deployed provider's firing and recovery messages and the
external monitor's missing-heartbeat alarm. Retain those receipts with their
test identity and observation times. A local drill report alone does not
qualify production providers or an independent external monitor.
