# FaasScheddInstanceStatsPartialErrors

## Symptom

This page means an owner schedd cannot project fresh vmmd telemetry for one or more durable instances on its own compute node.

## Check

1. Confirm the affected node is healthy in the compute registry and that `up{job="schedd-compute",node_id="<id>"}` and `up{job="vmmd",node_id="<id>"}` are both `1`.
2. Inspect schedd logs for capacity-stream disconnects and compare the instance rows' `node_id` with the alert label.

## Recover

1. Restart or repair the local vmmd-to-schedd capacity stream if it is stale. Do not treat errors from a different node as local failures; each schedd reports only its owned instances.
2. Verify `schedd_instance_{cpu_pct,rss_mb,inflight_requests}` returns within five seconds and the counter stops increasing.
