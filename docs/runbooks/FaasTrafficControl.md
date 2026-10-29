# FaasTrafficControl

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`traffic_control` (ADR-570).

Metrics: `gateway_traffic_refusals_total{path,reason}` from gatewayd-internal,
and `gatewayd_public_traffic_security_enforced`,
`gatewayd_public_traffic_security_legacy_gateways` and
`gatewayd_public_traffic_security_unverified_total` from the public edge.

Alerts:

- `FaasTrafficControlRefusing` (page): a fail-closed traffic control is
  refusing more than 0.2 requests per second for five minutes.
- `FaasPublicTrafficSecurityUnverified` (warn): a public edge has passed
  responses without the revocation check for 30 minutes.
- `FaasRateLimitCounterFallback` (warn): a gateway cannot reach the central
  rate counters and is enforcing limits from its own buckets inside the
  5-minute outage grace (`gateway_ratelimit_local_fallback_total`,
  ADR-20261027173012).

## Symptom

Customers see 503 problems whose code names the control, not their app:

| `reason` label | Customer code | Control |
| --- | --- | --- |
| `policy_unavailable` | `traffic_policy_unavailable` | Public routing or host policy could not be read or verified. |
| `security_unavailable` | `traffic_revocation_unavailable` | The revocation registry could not verify the account, app or deployment. |
| `rate_limit_unavailable` | `rate_limit_unavailable` | The central rate counters could not be consumed. |
| `node_admission_unavailable` | `http_admission_unavailable` | vmmd could not verify the instance's request cap. |

`rate_limited`, `deadline` and `node_capacity` are customer limits and do
not page.

For `FaasPublicTrafficSecurityUnverified`, responses still flow, but a revoked
account, app or deployment is not cut off at that edge.

## Check

Find which reason, path and gateway are refusing:

```promql
sum by (reason, path, instance) (rate(gateway_traffic_refusals_total{job="gatewayd-internal"}[5m]))
```

Then match the refusal log lines on the same gateway:

```
journalctl -u faas-gatewayd-internal --since -15m | grep -E 'refused with policy_unavailable|traffic_policy|revocation|rate limit|admission'
```

- `policy_unavailable`: the log line carries `stage` (owner, weights,
  release, revision, host pin). Check control-plane Postgres latency and
  errors; routing reads have a 250 ms bound. A single app refusing usually
  means its routing rows disagree (no live deployment in the routed scope),
  not an outage.
- `security_unavailable`: the revocation refresher has not verified the scope
  within the 5-minute outage grace. Check Postgres reachability from the
  gateway and the refresher's errors.
- `rate_limit_unavailable`: the central counter has failed for longer than
  the 5-minute outage grace (before that the gateway decides locally and
  `FaasRateLimitCounterFallback` warns). Check `pg_ratelimit_counters` lock
  waits, pool saturation and control-plane CPU pressure.
- `node_admission_unavailable`: vmmd has no plan for the instance. Check the
  instance's wake plan and whether vmmd restarted without adopting it.

For the edge alert, list the gateways without the fence:

```promql
gatewayd_public_traffic_security_legacy_gateways
```

The edge also logs each decision change with the gateways that lack the
wiring.

## Recover

- Postgres outage or saturation: restore the control-plane database. Policy
  and counter refusals clear on the next request; revocation clears on the
  next successful refresh.
- One app refusing `policy_unavailable`: fix its routing rows (redeploy or
  promote a live deployment into the routed scope). Do not disable the check.
- `node_admission_unavailable` after a vmmd restart: park and re-wake the
  affected instances so they start under a fresh wake plan.
- Unverified edge: finish rolling out the compute gateways it dials, or
  remove a retired node from the compute roster. The edge returns to enforcing
  within one observation interval (2 s) once every member reports the wiring.

## Escalate

If refusals continue after the store recovers, collect the refusing
gateway's logs and a `gregale` trace of one refused request (its span carries
`gregale.traffic.rejection_reason` and `gregale.traffic.policy_stage`) and
escalate to the gateway owner.
