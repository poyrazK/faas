# FaasServingProbe

Covers `FaasServingProbeFailing`, `FaasServingProbeStale`,
`FaasServingProbeCoverageDegraded` and `FaasServingProbeInventoryUnavailable`
(families `serving_probe` and `serving_probe_coverage`). Setup and configuration live in
`docs/ops/serving-probe.md`.

`faas-serving-probe.timer` on each compute node requests the platform-owned
probe app through the public edge every 5 minutes.

- **How a failure is reported:** the node posts it straight to the chat
  webhook, a path that works with the control plane down.
- **What these alerts add:** they mirror the same result into Alertmanager
  and the dashboards while the control plane is up, and notice a probe that
  stopped running.

## Symptom

- **`FaasServingProbeFailing`.** For 10 minutes, every run on that node failed
  status, configured body or first-byte checks on two attempts 30 s apart.
  A customer request through the public edge to a parked app may be failing
  or slow in the same way.
- **`FaasServingProbeStale`.** The node has not completed a probe run for 15
  minutes. An outage seen from there would go unreported.
- **`FaasServingProbeCoverageDegraded`.** An expected node/URL has been missing,
  stale, invalid, misconfigured or failing for ten minutes. Its numeric state
  matches the status table in the fleet dashboard and setup guide.
- **`FaasServingProbeInventoryUnavailable`.** The independent inventory cannot
  establish expected coverage. An empty coverage table does not imply health.

## Check

1. **How many nodes report it:**

   ```promql
   faas_serving_probe_success
   faas_serving_probe_http_status
   faas_serving_probe_content_check_enabled
   faas_serving_probe_first_byte_seconds
   faas_serving_probe_first_byte_budget_seconds
   ```

   - All compute nodes failing points at the public edge, Cloudflare or the
     probe app itself.
   - One node failing points at that node's network or DNS.
2. **The status:**
   - `0` means nothing answered: a timeout, a refused connection or a TLS
     failure. The node's journal has the error.
   - `5xx` came from the edge or the gateway.
   - `3xx` means the hostname is misrouted.
   - `2xx` with a failed probe means the configured body did not match or
     the first-byte/body deadline was exceeded. The journal names the failed
     check without recording response content. A content-check gauge of `0`
     means this node still uses status-only content validation.

   ```bash
   journalctl -u faas-serving-probe.service -n 30 --no-pager
   ```

3. **The probe app:** confirm it still exists, can wake, and has not been
   parked permanently by a plan or quota change (`gregale apps` in the
   platform's internal account).
4. **A stale probe:**

   ```bash
   systemctl status faas-serving-probe.timer faas-serving-probe.service
   ```

   The service refuses to start without `/etc/faas/serving-probe.env`.
   The role removes that file when `faas_serving_probe_urls` is empty.
5. **Coverage or configuration drift:** compare `faas_serving_probe_fleet` on
   the control plane and compute node. Check the registry node name and exact
   URL, then the content-check flag and budget. Missing validation metadata
   can mean the node still runs an older probe helper.
6. **Inventory unavailable:** check control-plane node_exporter and
   `/var/lib/node_exporter/textfile_collector/serving-probe-inventory.prom`.
   Converge the `prometheus` role with the shared policy; it installs
   `serving_probe_inventory`. The configured flag must be unique and fresh,
   and the declared pair count must match the inventory. `null` means
   unconfigured; `{}` explicitly expects zero probes.

## Recover

- **A real serving outage:** follow the edge and gateway runbooks for the
  status seen (`FaasGatewayQueueBacklogGrowing`, `FaasApiAvailabilityLow`).
  The probe clears by itself once a run passes, and the node posts a
  recovery message.
- **Probe app gone or misconfigured:** redeploy it per
  `docs/ops/serving-probe.md`. Keep its idle timeout shorter than the probe
  interval, so it parks between probes and never blocks a node drain.
- **Stale timer:** `systemctl restart faas-serving-probe.timer`, then converge
  the compute node if the unit or env file is missing.
