# builderd_service ansible role

Drops the builderd systemd unit + example TOML + FAAS_NODE_NAME/
FAAS_BUILDERD_ROLE drop-ins. Does NOT enable or start the daemon — the
operator runs `systemctl enable --now faas-builderd` once the release
bundle and operator-provisioned database/TLS configuration are present.

On named compute nodes the role also requires
`/etc/faas/runtime-bases.env` as `root:root 0600`, with an immutable digest
for every supported runtime. The populated file is the shared contract
between builderd and imaged; the role installs only an example, so release
automation must render the real digests before enabling either daemon.

The per-node CapacityReport signing key is owned by vmmd at
`/etc/faas/secrets/vmmd/node.key`; builderd does not load a separate node
key. Build attestations are handled by imaged's signing key.

## Drop-ins

- `99-faas-node-name.conf.j2` (linked from `_shared/`) — exposes this box's
  compute_node identity to builderd so the `build_claimed` log lines carry
  the canonical host name. Empty `FAAS_NODE_NAME` falls through to the
  short-hostname default — builderd's `main.go` reads the env var on first
  dial to label `build_artifact` rows in the deployments + build_runs
  tables.
- `99-faas-role.conf.j2` — wires the per-box role gate through to builderd
  via `FAAS_BUILDERD_ROLE` so `cmd/builderd/config.go::LoadConfig` picks up
  `faas_box_role` (compute-only on fsn-2). Without this drop-in builderd
  falls back to `RoleSingleBox` and the per-daemon role gate is unenforced.

## Slice + cgroup

builderd ships with `Slice=faas-cp.slice` and `MemoryMax=512M` on the unit
(sibling to vmmd's `MemoryMax=` ceiling). The builder VM runs INSIDE an
ephemeral builder microVM (ADR-003) — the 512M cap is the host-side guard
rail against the build process running wild before the VM cap kicks in.

## ReadWritePaths

`/srv/fc/builder`, `/srv/fc/base`, `/var/log/faas`, `/var/spool/faas`. The
role's `tasks/main.yml` asserts the unit's `ReadWritePaths=` exactly
matches this set so a drift in the `pkg/daemonunitspec/builderd.go`
constructor surfaces during `make bootstrap` rather than at first build.
