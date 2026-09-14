# FaasGCPOpsAgentMissing

This internal alert fires when a production compute node is reachable through
node-exporter but does not report an active `google-cloud-ops-agent.service`.
The separate Cloud Monitoring policy `Gregale Ops Agent export failures`
covers an installed collector that cannot deliver logs.

## Triage

1. Drain the affected compute node from new placements.
2. Check `systemctl status google-cloud-ops-agent.service` and
   `journalctl -u google-cloud-ops-agent.service` on the host.
3. Confirm the instance service account has `roles/logging.logWriter` and
   `roles/monitoring.metricWriter`, with the required OAuth scopes.
4. Run the normal `cd-compute` rollout. The common GCP Ops Agent role installs
   and starts the collector, and node readiness refuses an inactive unit.
5. In Cloud Logging, find a fresh `gregale-qualification` syslog entry for the
   instance before returning the node to customer capacity.

Do not disable local journal limits while export is unavailable. Preserve the
bounded local evidence and copy the incident window off-host if recovery takes
longer than the retention window.
