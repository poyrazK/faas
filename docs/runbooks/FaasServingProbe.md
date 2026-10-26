# FaasServingProbe

Covers `FaasServingProbeFailing` and `FaasServingProbeStale` (family
`serving_probe`, ADR-20261023124955 §5). Setup and configuration live in
`docs/ops/serving-probe.md`.

`faas-serving-probe.timer` on each compute node requests the platform-owned
probe app through the public edge every 5 minutes.

- **How a failure is reported:** the node posts it straight to the chat
  webhook, a path that works with the control plane down.
- **What these alerts add:** they mirror the same result into Alertmanager
  and the dashboards while the control plane is up, and notice a probe that
  stopped running.

## Symptom

- **`FaasServingProbeFailing`.** For 10 minutes, every run on that node missed
  a 2xx on two attempts 30 s apart. A customer request through the public edge
  to a parked app is likely failing the same way.
- **`FaasServingProbeStale`.** The node has not completed a probe run for 15
  minutes. An outage seen from there would go unreported.

## Check

1. **How many nodes report it:**

   ```promql
   faas_serving_probe_success
   faas_serving_probe_http_status
   ```

   - All compute nodes failing points at the public edge, Cloudflare or the
     probe app itself.
   - One node failing points at that node's network or DNS.
2. **The status:**
   - `0` means nothing answered: a timeout, a refused connection or a TLS
     failure. The node's journal has the error.
   - `5xx` came from the edge or the gateway.
   - `3xx` means the hostname is misrouted.

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
