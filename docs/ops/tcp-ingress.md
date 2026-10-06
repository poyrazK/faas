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
then create a listener through `gregale apps tcp <slug> add --name <name> --guest-port <port>` (or the API) and test
the allocated port from an allowed source. If an enabled listener cannot bind
at daemon startup, the daemon now fails startup and logs the port error. Do not
set `0.0.0.0/0` in staging.

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

The named port must already be declared on the app. List durable endpoints with
`gregale apps tcp <slug> list`; use `enable NAME` or `disable NAME` to change
exposure and `rm NAME` to remove the endpoint. The public address is the configured
edge address plus the returned public port, rather than an instance address.
TLS is passed through to the workload; gateway-to-daemon TLS settings protect
the internal transport and do not enable public per-listener TLS termination.
UDP declarations and node-local host-port leases do not create public datagram
listeners.

Qualification requires the native `TestTCPIngressMetal` result from the
container lane, plus the deployment's gateway/firewall configuration and an
external probe from an allowed source. Portable router/supervisor/limiter tests
cannot establish that the host firewall permits public traffic.

Listener reconciliation tracks app, account, listener name, guest port, and
protocol as well as the public port. If an endpoint is reassigned between
refreshes, the edge cancels the previous owner's sessions and rebinds the socket
for the new identity. Disabling or deleting an endpoint also cancels sessions;
gateway shutdown continues to use its separate graceful-drain contract.

`FAAS_TCPD_MAX_CONNECTIONS`, when positive, bounds concurrent sessions across
all listener ports managed by the gateway. Adding a public listener does not
multiply that global budget. The per-account cap remains independent; standalone
TCP servers retain their local cap. Sessions release global slots on completion
or cancellation.

New sessions check current app ownership, deletion and maintenance mode before
selecting a running instance or requesting admission. Maintenance rejects new
sessions without waking the app; clearing it restores routing. Established
connections keep their ordinary bounded lifetime. Disable the listener to cancel
them immediately. Target rotation uses a bounded atomic counter, so deleted apps
do not leave a growing per-app cursor cache at the edge.

Admission also re-reads durable listener intent. A stale cached route cannot open
a new session after disable or a change to its public port, guest port, app,
account or protocol. Reconciliation still closes already established sessions
when the listener changes.

TLS termination is opt-in per listener. Set `FAAS_TCPD_TLS_CERT_DIR` (Ansible:
`faas_tcpd_tls_cert_dir`) to an absolute directory, preferably beneath `/etc/faas`.
The role validates existing real, root-owned directories without changing ownership
or mode. Only missing directories are created root:faas 0750. Paths containing
controls or dot components, and filesystem root, are rejected. The path is quoted
in the managed systemd environment file. The role does not issue or copy certificates. Provision `<normalized-hostname>.pem` containing the
certificate chain and matching private key with mode 0640 or 0600 and read access
for gatewayd-public. Replace complete bundles by atomic rename for rotation.
The provider pins the directory opened at startup. Replacing or renaming the
directory itself does not redirect lookups to a new directory; restart the edge
to select a replacement directory. Rotate bundle files within the pinned directory.
Bundles are limited to 64 KiB. Keep Caddy's certificate storage separate.
The certificate directory must not be writable by its group or other users.
Gateway startup checks this permission, and each certificate lookup rechecks it
so a later permission change fails closed. Operators must also secure directory
ownership, ancestor directories, and any filesystem ACLs.

Create with `gregale apps tcp APP add --name NAME --guest-port PORT --tls-mode
terminate --tls-hostname HOST`. The hostname must be a verified app-wide domain
owned by that app, and the listener starts disabled. Provision the bundle before
enabling. Missing/invalid bundles, incorrect SNI and expired certificates reject
the connection before a wake. Directory configuration errors fail gateway startup;
individual missing bundles fail only their handshakes. Existing sessions retain
their negotiated certificate when bundles rotate. Native qualification remains
pending; socket readiness alone does not establish certificate readiness.

Change an existing listener with `gregale apps tcp APP tls NAME --tls-mode
terminate --tls-hostname HOST`, or use `--tls-mode passthrough` to restore raw
forwarding. Every TLS policy update atomically disables the endpoint, cancels
sessions during reconciliation, and requires an explicit enable afterward.
Provision the new policy first; configuration and enable are separate mutations.

The edge exports aggregate `gatewayd_public_tcp_tls_listeners_ready`,
`gatewayd_public_tcp_tls_listeners_not_ready` and
`gatewayd_public_tcp_tls_certificate_expiry_seconds`. Reconciliation checks the
configured provider, current hostname/validity, matching signing key and server
authentication usage; expiry is the earliest among
ready enabled TLS endpoints, or zero when none are ready. These metrics describe
certificate readiness separately from initial socket binding and reset after
disable/shutdown. They do not certify domain routing, client trust, issuance or
guest availability. Hostnames are not metric labels.

The edge also publishes per-listener certificate observations to a separate
durable table, using `FAAS_NODE_NAME` as its identity (`legacy-singlebox` when
unset in the single-node deployment). Readiness changes publish immediately;
unchanged evidence refreshes every fifteen seconds. Evidence expires after sixty
seconds and is bound to the exact listener policy revision. Publication has a
two-second cycle budget and stale rows are pruned. Read customer-safe evidence
with `gregale apps tcp APP tls-status NAME` (or `--json`), backed by authenticated
`GET /v1/apps/{slug}/tcp-listeners/{name}/tls-status`. Empty observations mean
unknown status. Disabled or changed intent and stale/future observations produce
unknown per-edge status and omit expiry; an expired observed certificate produces
not-ready status. The response has scope `observed_edges` and must not be treated
as fleet-wide readiness, client trust, public routing or guest availability.

`FaasTCPTLSCertificateUnavailable` alerts after five minutes with enabled TLS
endpoints lacking valid bundles. `FaasTCPTLSCertificateExpiring` alerts after ten
minutes when the earliest ready certificate expires within seven days. The
expiry alert requires at least one ready TLS endpoint, so disabled/passthrough
edges with zero expiry do not trigger it. Run `make tcp-tls-alert-check` to verify
the rules and healthy/disabled cases before rollout.

Durable TCP routes retain the listener row ID through TLS negotiation and
instance admission. Deleting and recreating a listener with the same app, port
and TLS settings invalidates in-flight routes from the deleted row. The edge
rejects them before selecting or waking an instance. Reconciliation also treats
a changed listener ID as a replacement and cancels the old listener's sessions.

New raw TCP connections select a live deployment using its persisted traffic
weight, then choose a running non-mirror instance within that deployment. A cold
bucket is admitted by explicit deployment ID. Zero-weight and superseded rows
receive no new sessions, even while their instances remain running. Existing
connections retain their instance until normal teardown. Invalid serving weight
sets fail closed; see [ADR-391](../adr/391-raw-ingress-deployment-traffic.md).
