# Raw TCP ingress rollout

Gregale's raw TCP edge is deliberately disabled by default. Enable it per
staging host only after the guest workload declares a named TCP port.

Set the same rollout switch on the public gateway and the nftables role:

```yaml
# host_vars/staging-gateway.yml
faas_tcpd_enabled: true
faas_tcpd_bind_host: 0.0.0.0
faas_tcpd_allowed_cidrs:
  - 198.51.100.24/32 # staging tester or load balancer
# Optional safety bounds for long-lived raw sessions.
faas_tcpd_idle_timeout: 60m
faas_tcpd_max_connections_per_account: 64
```

The gateway role renders `/etc/faas/tcpd.env`, which is loaded by
`faas-gatewayd-public.service`. The nftables role admits only TCP ports
40000–49999 from the listed CIDRs; leaving the list empty keeps the host
fail-closed even if the daemon is enabled. The public port is allocated by the
TCP listener API and remains stable while the app instance parks or wakes.
The idle timeout resets whenever bytes cross the edge; the account-scoped
session cap is enforced independently for each account on the gateway.

During a gateway restart, SIGTERM closes the raw-TCP listener sockets first and
lets accepted sessions finish within the shared gateway drain budget. A second
signal or an expired budget force-closes the remaining sessions.

For split-box deployments, set the `faas_tcpd_schedd_target` and the optional
`faas_tcpd_*_tls_*_path` variables in the gateway role. Single-box installs
use the local schedd Unix socket by default.

After convergence, verify the daemon log contains `raw TCP ingress enabled`,
then create a listener through `gregale app tcp create` (or the API) and test
the allocated port from an allowed source. Do not set `0.0.0.0/0` in staging.

The public gateway publishes TCP runtime telemetry on its existing `/metrics`
endpoint. The `gatewayd_public_tcp_active_sessions` gauge shows current load,
`gatewayd_public_tcp_active_sessions_by_account` shows account-scoped usage,
and the `gatewayd_public_tcp_sessions_*` counters distinguish accepted,
completed, and rejected sessions. `gatewayd_public_tcp_bytes_total` reports
both directions, while `gatewayd_public_tcp_session_duration_seconds` and
`gatewayd_public_tcp_idle_timeouts_total` cover latency and idle reaping.
The fleet dashboard panels 418-419 graph these signals, and Prometheus alerts
on sustained account-quota rejection or idle-timeout spikes; see
`docs/runbooks/FaasTCPIngress.md` for triage.
