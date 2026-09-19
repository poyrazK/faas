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
```

The gateway role renders `/etc/faas/tcpd.env`, which is loaded by
`faas-gatewayd-public.service`. The nftables role admits only TCP ports
40000–49999 from the listed CIDRs; leaving the list empty keeps the host
fail-closed even if the daemon is enabled. The public port is allocated by the
TCP listener API and remains stable while the app instance parks or wakes.

For split-box deployments, set the `faas_tcpd_schedd_target` and the optional
`faas_tcpd_*_tls_*_path` variables in the gateway role. Single-box installs
use the local schedd Unix socket by default.

After convergence, verify the daemon log contains `raw TCP ingress enabled`,
then create a listener through `gregale app tcp create` (or the API) and test
the allocated port from an allowed source. Do not set `0.0.0.0/0` in staging.
