# `prometheus` ansible role

Installs the Prometheus binary pinned to a specific version
(`prom_version`, `prom_release_sha256` in `defaults/main.yml`), drops
a scrape config that pulls from every faas daemon + `node_exporter`,
and runs it as a hardened systemd unit on the bridge IP.

## Scrape targets (spec §12)

- `apid`      `:9092`
- `gatewayd`  `:9093`
- `schedd`    `:9091` (also exposes `/metrics/fcvm`)
- `vmmd`      `:9104` (also exposes `/metrics/fallback`)
- `imaged`    `:9095`
- `node`      `:9100` (node_exporter on the bridge IP)

## Override at invocation

```bash
ansible-playbook -e prom_version=2.55.0 \
                 -e prom_release_sha256=<new-sha> site.yml
```

## Hardening (spec §11)

`NoNewPrivileges`, `PrivateTmp`, `ProtectSystem=strict`,
`ProtectHome`, `ReadWritePaths={{ prom_data_dir }}`, kernel tunables
+ modules + cgroups protected. The binary runs as the `prometheus`
system user.

## Deferred

- TLS auth on `/metrics` (per-daemon listener sockets are unix-only;
  Prometheus scrapes via loopback, so the public threat model is
  the bridge address). Re-evaluate when M5 adds public listeners.
- Remote write to a long-term store (deferred to M9 — Hetzner Storage
  Box or a small VM in FRA1).