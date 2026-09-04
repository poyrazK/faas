# compute_only_service ansible role

Drops the imaged systemd unit + example TOML + role/routing drop-ins. Does
NOT enable or start imaged — the operator runs
`systemctl enable --now faas-imaged` once `/etc/faas/compute-db.env` is
populated with both `DATABASE_URL` and `FAAS_VMMD_DBURL` (gap G2).

## Drop-ins

- `99-faas-node-name.conf.j2` (linked from `_shared/`) — exposes this box's
  compute_node identity to imaged (and builderd, when co-installed on the
  compute-only box) so the role gate + boot log carry the right name.
  imaged has no `config.go` today (env-only); the env is the only source.
- `99-faas-role.conf.j2` — wires the per-box role gate through to imaged via
  `FAAS_IMAGED_ROLE` so `cmd/imaged/main.go::LoadConfig` picks the right
  `role.FromConfig` sentinel. Without this drop-in imaged falls back to
  `RoleSingleBox` on the compute-only box and the cosign sign-keypair path
  assumes single-box assumptions (no per-box PKI subset).
- `zz-faas-vmmd-client.conf.j2` — owns the split-box imaged → vmmd target and
  dedicated `imaged/vmmd-client` leaf. The target is the compute host's
  stable private endpoint, not a shared `vmmd.faas` resolver alias. The vmmd
  server leaf retains `vmmd.faas` as its role identity and carries the
  endpoint as an additional SAN, so adding another compute box cannot route
  imaged to the wrong node.

## Side effects

- Creates the `faas` group (shared with the vmmd role).
- Ensures `/etc/faas` exists (mode 0750 root:faas).
- Installs the imaged example TOML to `/etc/faas/imaged.toml.example`
  (operator copies to `imaged.toml`).
- Installs the systemd unit to `/etc/systemd/system/faas-imaged.service`.
- Installs the shared `storage.env.example`; the provider-neutral node join
  pipeline stages the populated `/etc/faas/storage.env` for OCI-backed
  multi-box deployments.
- Installs `runtime-bases.env.example` and, on named compute nodes, refuses
  convergence until `/etc/faas/runtime-bases.env` is `root:root 0600` with
  digest-pinned refs for all supported runtimes. imaged and builderd consume
  this same file so a node cannot build against one base and stage against
  another.
- **Mega-PR-C**: chowns `/run/faas` to root:faas 0775 at deploy time AND
  ships a `/etc/tmpfiles.d/faas.conf` rule so the same ownership
  survives every reboot. Mirrors `control_plane_service`'s PR-D + PR-M
  pattern (the same runtime dir + ownership on fsn-1). The role's
  trips here so the vmmd unit's `RuntimeDirectory=faas` (declared on
  the vmmd unit alone — the comment in
  `deploy/systemd/faas-vmmd.service` explains why only one faas unit
  may declare it) creates the dirent with the canonical ownership
  before the first scheduler tick. The vmmd unit's own `ExecStartPre`
  chown remains as defense-in-depth for the edge case where someone
  wipes `/run` between reboots without the tmpfiles-d firing.
