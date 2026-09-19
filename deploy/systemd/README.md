# Systemd units

This directory contains the checked-in systemd units used by the split-box
deployment. The production installer renders and installs the role-specific
copies through Ansible; do not revive the removed monolithic `gatewayd` unit.

The edge is two services:

- `faas-gatewayd-public.service` (and its socket) owns the public listener,
  receives traffic from the upstream Caddy/Cloudflare edge, and hands it to
  the node-local gateway. TLS termination is upstream of this plain-HTTP daemon.
- `faas-gatewayd-internal.service` owns routing, wake coordination, and proxying
  on the node-local socket.

`faas-gatewayd-public.service` also loads the optional
`/etc/faas/tcpd.env`. Set `FAAS_TCPD_ENABLED=1` there only alongside the
nftables rule that admits the reserved 40000–49999 listener range; the Ansible
role renders this file from the `faas_tcpd_*` variables.

The remaining units are installed by their owning role: `apid`, `schedd`,
`vmmd`, `builderd`, `imaged`, `meterd`, `outboundd`, and `realtimed`, plus the
PostgreSQL backup and WAL-prune timers. The control-plane and compute-only
plays intentionally install different daemon sets and mask the opposite role.
See [`deploy/ansible/README.md`](../ansible/README.md) for the supported
bootstrap and verification flow.

## Manual inspection

To inspect the generated units on a host:

```sh
systemctl cat faas-gatewayd-public.service
systemctl cat faas-gatewayd-internal.service
systemctl --type=service --state=running 'faas-*'
```

For a development-only install from this checkout, install the units for the
role you are testing, then reload systemd:

```sh
sudo install -m 0644 deploy/systemd/faas-gatewayd-public.service /etc/systemd/system/
sudo install -m 0644 deploy/systemd/faas-apid.socket /etc/systemd/system/
sudo install -m 0644 deploy/systemd/faas-gatewayd-public.socket /etc/systemd/system/
sudo install -m 0644 deploy/systemd/faas-gatewayd-internal.service /etc/systemd/system/
sudo systemctl daemon-reload
```

Do not enable a unit until its role's configuration, secrets, sockets, and
dependencies have been rendered by the Ansible play. `gregalectl doctor` and
`make verify-fleet` are the supported post-install checks.
