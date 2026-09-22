# Raw TCP ingress telemetry

Use this runbook when `FaasTCPIngressQuotaRejections` or
`FaasTCPIngressIdleTimeoutSpike` fires.

## Inspect the gateway

Confirm the public gateway is serving its control endpoint and inspect the
raw TCP series:

```sh
curl -fsS http://127.0.0.1:9092/metrics \
  | grep -E 'gatewayd_public_tcp_(active_sessions|sessions_rejected|idle_timeouts|bytes_total)'
```

Useful PromQL:

```promql
sum by (reason) (rate(gatewayd_public_tcp_sessions_rejected_total[5m]))
topk(20, gatewayd_public_tcp_active_sessions_by_account)
sum(rate(gatewayd_public_tcp_idle_timeouts_total[5m]))
sum by (direction) (rate(gatewayd_public_tcp_bytes_total[5m]))
```

## Quota rejections

For account-limit rejections, compare the affected account's
`gatewayd_public_tcp_active_sessions_by_account` value with
`FAAS_TCPD_MAX_CONNECTIONS_PER_ACCOUNT`. Check whether clients are leaking
connections or opening a burst of long-lived sessions before increasing the
quota. The setting is rendered by the gatewayd-public Ansible role and
requires a service restart to change.

## Idle timeout spikes

Check `FAAS_TCPD_IDLE_TIMEOUT` and the client protocol's keepalive behavior.
The timer resets only when payload bytes cross the public edge; TCP-level
keepalive packets do not count as application activity. Restore client or
downstream service progress before increasing the timeout, since a larger
value consumes more gateway connection capacity.
