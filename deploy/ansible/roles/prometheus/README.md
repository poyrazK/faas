# `prometheus` ansible role

Installs the Prometheus binary pinned to a specific version
(`prom_version`, `prom_release_sha256` in `defaults/main.yml`), drops
a scrape config that pulls from every control-plane daemon, the private
compute gateways, and `node_exporter`, and runs it as a hardened systemd
unit on loopback.

## Scrape targets (spec §12)

- `apid`      `:9101`
- `gatewayd-public` control listener `:9092`
- `schedd`    `:9103` (canonical `/metrics`; `/metrics/fcvm` remains an alias)
- `vmmd`      `:9104` (canonical `/metrics`; `/metrics/fallback` remains an alias)
- `imaged`    `:9102`
- `builderd` `:9105` on single-box installs (the daemon's canonical default)

On a split deployment, compute gateway, vmmd, imaged, builderd and Promtail
metrics are discovered through separate apid loopback HTTP service-discovery
endpoints backed by the active `compute_nodes` registry. The manifest renderer
binds each compute daemon's metrics listener to that node's private transport
address. Adding, draining, or replacing a compute node therefore does not
require editing the Prometheus target list or restarting Prometheus. The public
gateway explicitly rejects the internal endpoints.
- `meterd`    `:9106`
- `outboundd` `:9108` (private request-admission and upstream metrics)
- `prometheus` `:9095` (loopback self-scrape for alerting-path health)
- `githubd`   `:8083`
- `alertmanager` `:9094`
- `node`      `:9100`
- `gatewayd-internal` each compute node `:8080/v1/internal/metrics` through its
  private control-plane allowlist. Targets use generated `faas_node_name`
  aliases, not provider-specific IP addresses.

## Override at invocation

```bash
ansible-playbook -e prom_version=2.55.0 \
                 -e prom_release_sha256=<new-sha> bootstrap.yml
```

## Hardening (spec §11)

`NoNewPrivileges`, `PrivateTmp`, `ProtectSystem=strict`,
`ProtectHome`, `ReadWritePaths={{ prom_data_dir }}`, kernel tunables
+ modules + cgroups protected. The binary runs as the `prometheus`
system user.

Prometheus listens on `127.0.0.1:9095`. The compute
`/v1/internal/metrics` route is
available only on the private compute data-plane listener, and the generated
nftables policy allows that port from the control plane. It is not added to
the public edge or to provider DNS. `/metrics` remains an ordinary
customer-workload path on app hostnames.

The systemd unit is rendered as a template. This is required because its
storage path, retention, and listen address are Jinja variables; copying the
file verbatim leaves literal `{{ ... }}` arguments and causes a restart loop.

UDP ingress metrics are exported on the existing gatewayd-public operator scrape
endpoint with the `gatewayd_public_udp_` prefix. They include active peers and
sockets, peer completion outcomes/duration, inbound queued and outbound sent
payload/datagram counters, drops by fixed reason, and listener reconciliation
errors. Empty datagrams count as packets with zero payload bytes. Labels do not
contain client addresses, app/account IDs or error text.

The `faas_udp_ingress` alert group covers sustained admission/forwarding failures,
listener reconciliation errors, and drops caused by peer/queue/rate pressure.
Source-denied packets, expected idle expiry and `resource_exhausted` completions
do not trigger the peer-failure alert. Resource exhaustion has its own sustained
pressure alert and warning-level gateway log. Packet counters describe socket queue/send boundaries; they do not prove
application delivery or UDP reliability. Check current gateway errors and native
acceptance evidence before enabling a new UDP workload publicly.

Run `make udp-alert-check` to validate rules and the UDP alert scenarios.
# Raw TCP TLS certificate alerts

The `faas_tcp_tls` group separates certificate readiness from public socket
readiness. It warns on missing/invalid enabled-listener certificates for five
minutes and ready certificates within seven days of expiry for ten minutes.
Metrics are aggregate per edge; hostname labels are not introduced. Expiry
requires a positive ready-listener count, keeping disabled/passthrough nodes
quiet. `make tcp-tls-alert-check` validates failure, expiry and normal scenarios.
