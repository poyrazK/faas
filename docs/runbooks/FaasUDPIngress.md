# FaasUDPIngress

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, group
`faas_udp_ingress`. Severity: warn. Family: `udp_ingress`.

## Symptom

`FaasUDPListenerReconciliationFailing` reports reconciliation errors over ten
minutes that persist for five minutes. `FaasUDPPeerFailuresHigh` reports sustained
admission or forwarding failures. `FaasUDPResourceDropsHigh` reports sustained
peer, queue or rate pressure; `FaasUDPPeerResourceExhaustionHigh` reports sessions
ending at their transport resource limits.

UDP packet counters measure queue/send boundaries and do not prove guest
application delivery. Empty datagrams count as packets with zero payload bytes.
Expected idle expiry and source-denied traffic are excluded from peer-failure
alerts. Resource exhaustion has a separate alert.

## Check

Inspect `journalctl -u gatewayd-public --since '15 minutes ago'` on the affected
host. Compare reconciliation errors, active sockets and peers, completion
outcomes and drop reasons from the existing gatewayd-public operator scrape.
Metrics deliberately omit client addresses and tenant IDs; use authenticated
listener listings and gateway logs to identify affected workloads.

For reconciliation errors, check PostgreSQL connectivity, enabled durable UDP
intent, unique port ownership and bind conflicts. Disabled intents reserve ports
but should not own listening sockets. Verify `/etc/faas/udpd.env` is loaded by the
unit and that the bind host is an IPv4 literal.

For admission/forwarding failures, check the private scheduler target and TLS
configuration, serving deployment's declared UDP port, VMMD transport health and
availability of the deployed `vmmd-udp-bridge` helper. Review cold admission
latency separately from warm traffic. For resource pressure, inspect retry
volume, active peers and byte/message limits before changing any capacity.

## Recover

Restore failed database, scheduler or VMMD dependencies and correct invalid
intent or bind conflicts. Confirm listener reconciliation resumes before
restarting the public gateway: a restart interrupts existing peers. If one
workload causes pressure, disable its UDP listener through the authenticated API
or CLI while repairing its deployment or client retry behavior. Preserve its
reserved endpoint; do not delete another tenant's reservation.

Keep source CIDRs explicit and consistent between runtime and firewall. Do not
broaden source policy or raise queue/rate limits to hide failures. After recovery,
verify reconciliation errors stop increasing, expected socket ownership returns,
and a permitted client can exchange datagrams with the actual guest. Watch alert
recovery over the configured window. Public enablement requires native
Linux/amd64 KVM and leak acceptance; portable fixtures alone are insufficient.
