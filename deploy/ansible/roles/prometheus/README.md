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

On a split deployment, compute gateway metrics are discovered through apid's
loopback HTTP service-discovery endpoint, backed by the active `compute_nodes`
registry. Adding, draining, or replacing a compute node therefore does not
require editing the Prometheus target list or restarting Prometheus. The
public gateway explicitly rejects that internal endpoint.
- `meterd`    `:9106`
- `prometheus` `:9095` (loopback self-scrape for alerting-path health)
- `githubd`   `:8083`
- `alertmanager` `:9094`
- `node`      `:9100`
- `gatewayd-internal` each compute node `:8080/metrics` through its
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

Prometheus listens on `127.0.0.1:9095`. The compute `/metrics` route is
available only on the private compute data-plane listener, and the generated
nftables policy allows that port from the control plane. It is not added to
the public edge or to provider DNS.

The systemd unit is rendered as a template. This is required because its
storage path, retention, and listen address are Jinja variables; copying the
file verbatim leaves literal `{{ ... }}` arguments and causes a restart loop.
