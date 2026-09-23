# FaasServiceRolloutHandoffBlocked

## Symptom

This warning means schedd could not complete the routing acknowledgement or
request-drain barrier for a zero-downtime service rollout. The handoff fails
closed: both generations remain live and the scheduler retries from the
durable deployment state.

## Check

1. Open **Safe-releases** in the operator dashboard, or inspect the deployment
   JSON, and note `service_rollout_handoff.action`, `phase`, `generation`,
   `missing_gateways`, `retry_count`, and `last_error`.
2. For a `routing` failure, verify every listed compute node has a healthy
   serving gateway and can receive `deployment_route_changed` notifications.
   Compare the missing node list with the active compute-node registry.
3. For a `draining` failure, inspect vmmd in-flight-request telemetry for the
   deployment being retired. Missing or stale telemetry is intentionally not
   treated as zero.

## Recover

1. Do not manually park either generation. Restore the failed gateway or
   telemetry path and allow the bounded schedd recovery sweep to retry.
2. If an operator requested an abort, the candidate is retired only after the
   predecessor route is acknowledged and the candidate's requests drain.
