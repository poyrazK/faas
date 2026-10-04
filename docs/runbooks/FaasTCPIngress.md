# Raw TCP ingress telemetry

Use this runbook when `FaasTCPIngressQuotaRejections` or
`FaasTCPIngressIdleTimeoutSpike`, `FaasTCPTLSCertificateUnavailable`, or
`FaasTCPTLSCertificateExpiring` fires.

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

## Missing or expiring TCP certificates

Inspect `gatewayd_public_tcp_tls_listeners_ready`, `gatewayd_public_tcp_tls_listeners_not_ready`, and `gatewayd_public_tcp_tls_certificate_expiry_seconds` on the affected edge. These describe local certificate material and do not establish fleet coverage or guest readiness. Confirm the gateway's effective `FAAS_TCPD_TLS_CERT_DIR` and use `gregale apps tcp APP tls-status NAME` to correlate fresh observed-edge evidence with customer intent. Missing or stale observations remain unknown.

The directory must be a root-owned real directory without group or other write access for the Ansible role. Existing directories are validated without changing permissions; missing ones are created root:faas 0750. Provide the faas service account read/traverse access. A complete `HOST.pem` must contain the certificate chain and matching private key, fit within 64 KiB, and use 0600 or 0640 with appropriate read access. Do not copy or log private key material during triage.

Check certificate hostname, validity, key match, chain, and permitted usage. Renew through the operator's existing certificate issuer, stage the complete PEM bundle in the configured directory with final ownership and mode, then atomically rename it to `HOST.pem`. New handshakes load the replacement; existing sessions keep their negotiated certificate. Gregale does not issue certificates automatically. Keep this directory separate from Caddy's storage.

Disable an affected listener if no usable certificate can be provisioned. Policy changes disable the listener; re-enable separately after provisioning. Verify fresh per-edge evidence, metrics, and a real client handshake from an allowed source. An enabled socket alone is insufficient.

Run `make tcp-tls-alert-check tcp-tls-deployment-check` before deploying configuration changes. Native Linux/amd64 KVM acceptance remains a separate release gate.
