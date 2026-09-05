# githubd_service ansible role

Drops the githubd systemd unit + example TOML + FAAS_NODE_NAME/
FAAS_GITHUBD_ROLE drop-ins. Does NOT enable or start the daemon — the
operator runs `systemctl enable --now faas-githubd` once the GitHub App
credentials and `FAAS_GITHUB_WEBHOOK_SECRET` are provisioned in
`/etc/faas/secrets/githubd/githubd.env`.

## Drop-ins

- `99-faas-node-name.conf.j2` (linked from `_shared/`) — exposes this box's
  compute_node identity to githubd so the multi-box bridge handler reads
  the right name without a TOML edit.
- `99-faas-role.conf.j2` — wires the per-box role gate through to githubd
  via `FAAS_GITHUBD_ROLE` so `cmd/githubd/config.go::LoadConfig` picks the
  right `role.FromConfig` sentinel. Without this drop-in githubd falls
  back to `RoleSingleBox` on a multi-host fleet and the per-daemon role
  gate is unenforced.
