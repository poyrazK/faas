# githubd_service ansible role

Drops the githubd systemd unit + example TOML + FAAS_NODE_NAME/
FAAS_GITHUBD_ROLE drop-ins. Does NOT enable or start the daemon — the
operator runs `systemctl enable --now faas-githubd` once the GitHub App
credentials and `FAAS_GITHUB_WEBHOOK_SECRET` are provisioned in
`/etc/faas/secrets/githubd/githubd.env`.

Split-box inventories also install an mTLS gRPC listener at
`tcp://0.0.0.0:50053` for compute-only imaged branch freshness checks. The
listener is restricted to private compute CIDRs by nftables and requires the
fleet CA client certificate.

## Drop-ins

- `99-faas-node-name.conf.j2` (linked from `_shared/`) — exposes this box's
  compute_node identity to githubd so the multi-box bridge handler reads
  the right name without a TOML edit.
- `99-faas-role.conf.j2` — wires the per-box role gate through to githubd
  via `FAAS_GITHUBD_ROLE` so `cmd/githubd/config.go::LoadConfig` picks the
  right `role.FromConfig` sentinel. Without this drop-in githubd falls
  back to `RoleSingleBox` on a multi-host fleet and the per-daemon role
  gate is unenforced.
- `99-faas-source-ref-mtls.conf.j2` — enables the split-box listener and
  loads the `githubd/server` leaf plus fleet CA. Single-box installs keep the
  unix socket unless the listener variable is configured.
