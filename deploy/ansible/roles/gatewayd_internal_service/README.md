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
  compute_node identity to gatewayd-internal and realtimed. The realtimed
  daemon uses it to report resumable channel routes to apid.
- `99-faas-role.conf.j2` — wires the per-box role gate through to
  gatewayd-internal via `FAAS_GATEWAYD_ROLE` (note: NOT `_INTERNAL` — the
  env-var name matches the daemon's `pkg/role` lookup key) so
  `cmd/gatewayd-internal/config.go::LoadConfig` picks the right
  `role.FromConfig` sentinel. Without this drop-in gatewayd-internal falls
  back to `RoleSingleBox` on a multi-host fleet and the per-daemon role
  gate is unenforced.
- `99-faas-retry-budget.conf` — when `gatewayd_retry_budget_redis_url` is
  supplied from a common Ansible Vault variable, projects the root-only
  Redis URL through `LoadCredential` and selects the shared Redis retry backend.
  With no Redis override, central mode uses the shared Postgres retry backend
  (ADR-570); explicit local mode has no fleet-wide cap.
  Set `gatewayd_retry_budget_required=true` on every gateway for the
  activation pass; the role fails if any gateway lacks the URL. The URL is
  never written into the drop-in or logged by Ansible. The optional URL
  requires an operator-provisioned Redis service; this role does not
  install Redis. Follow `docs/runbooks/FaasSharedRetryBudget.md` for the
  rollout check and rollback procedure.

## Restart handler

The role restarts gatewayd-internal and realtimed when their FAAS_NODE_NAME
drop-ins change, so both daemons use the current node identity after the next
`ansible-playbook` run.

## See also

- `docs/adr/068-tier-a7-edge-split.md`
- `deploy/systemd/faas-gatewayd-internal.service`
- `cmd/gatewayd-internal/main.go`
