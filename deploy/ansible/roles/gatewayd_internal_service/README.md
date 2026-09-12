# gatewayd_internal_service ansible role

Installs the systemd unit for `gatewayd-internal`, the routing +
wake + proxy daemon introduced in the Tier A7 split (ADR-070), and
the managed realtime connection owner that serves its reserved
WebSocket namespace.
This role replaces the legacy `gatewayd_service` role for new
installs; operators on the legacy daemon can run both side by
side during the migration window.

## What this role does

1. Drops `/etc/systemd/system/faas-gatewayd-internal.service`.
2. Drops `/etc/systemd/system/faas-realtimed.service` and its role gate.
3. Runs `systemctl daemon-reload`.

## What this role does NOT do

- Provision the unix socket ACL. `/run/faas` is owned by
  `faas-vmmd.service` (the SOLE `RuntimeDirectory=faas` across the
  faas service set; see `faas-vmmd.service` for why declaring it
  here as well would create a second per-unit tmpfs whose
  bind-mount doesn't propagate back to `/run`). The daemon
  itself sets the per-socket mode to 0660 with group `faas` on
  first dial.
- Enable the unit (`systemctl enable --now faas-gatewayd-internal`).
- Run the daemon (it picks up on first start).

## Network surface

`gatewayd-internal` is **loopback-only** for inbound traffic. The systemd
unit's `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6` keeps the listener
local while allowing its required outbound paths. Inbound traffic is from
`gatewayd-public` over the unix socket; outbound traffic is gRPC to per-node
schedd/vmmd via `pkg/wire.DialContext` (loopback mTLS) and HTTPS delivery to
customer-configured log-drain endpoints. Runtime drain records are spooled
under `/var/lib/faas/log-drains` before delivery.

## Drop-ins

- `99-faas-node-name.conf.j2` (linked from `_shared/`) — exposes this box's
  compute_node identity to gatewayd-internal so the multi-box handshake
  layer reads the right name without a TOML edit.
- `99-faas-role.conf.j2` — wires the per-box role gate through to
  gatewayd-internal via `FAAS_GATEWAYD_ROLE` (note: NOT `_INTERNAL` — the
  env-var name matches the daemon's `pkg/role` lookup key) so
  `cmd/gatewayd-internal/config.go::LoadConfig` picks the right
  `role.FromConfig` sentinel. Without this drop-in gatewayd-internal falls
  back to `RoleSingleBox` on a multi-host fleet and the per-daemon role
  gate is unenforced.

## Restart handler

The role declares a `notify: restart faas-gatewayd-internal` handler on the
FAAS_NODE_NAME drop-in install so a name change is picked up immediately
on the next `ansible-playbook` run. Other daemons in this role cluster do
NOT restart on drop-in change — the operator's `systemctl enable --now`
loop is the contract there.

## See also

- `docs/adr/068-tier-a7-edge-split.md`
- `deploy/systemd/faas-gatewayd-internal.service`
- `cmd/gatewayd-internal/main.go`
