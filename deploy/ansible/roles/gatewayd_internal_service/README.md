# gatewayd_internal_service ansible role

Installs the systemd unit for `gatewayd-internal`, the routing +
wake + proxy daemon introduced in the Tier A7 split (ADR-070).
This role replaces the legacy `gatewayd_service` role for new
installs; operators on the legacy daemon can run both side by
side during the migration window.

## What this role does

1. Drops `/etc/systemd/system/faas-gatewayd-internal.service`.
2. Runs `systemctl daemon-reload`.

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

`gatewayd-internal` is **loopback-only**. The systemd unit's
`RestrictAddressFamilies=AF_UNIX` enforces this at the kernel
syscall layer — even a buggy code path can't reach an external
IP. The only inbound traffic is from `gatewayd-public` over the
unix socket; the only outbound traffic is gRPC to per-node
schedd/vmmd via `pkg/wire.DialContext` (loopback mTLS).

## See also

- `docs/adr/068-tier-a7-edge-split.md`
- `deploy/systemd/faas-gatewayd-internal.service`
- `cmd/gatewayd-internal/main.go`