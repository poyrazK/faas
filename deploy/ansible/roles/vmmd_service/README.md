# vmmd_service ansible role

Drops the vmmd systemd unit + example TOML + FAAS_NODE_NAME/FAAS_VMMD_ROLE
drop-ins. Does NOT enable or start the daemon — vmmd is the only root
component and runs only on the EX44 hardware. The operator runs
`systemctl enable --now faas-vmmd` once the box has been provisioned with a
/srv/fc/base kernel and the firecracker binary.

## Drop-ins

- `99-faas-node-name.conf.j2` (linked from `_shared/`) — exposes this box's
  compute_node identity to vmmd via `FAAS_NODE_NAME` so the
  `[compute_node].name` self-registration (`cmd/vmmd/register.go`) writes the
  right row at startup. Empty `FAAS_NODE_NAME` falls through to the
  short-hostname default — the vmmd self-register path retains that
  back-compat.
- `99-faas-role.conf.j2` — wires the per-box role gate through to vmmd via
  `FAAS_VMMD_ROLE` so `cmd/vmmd/config.go::LoadConfig` picks up
  `faas_box_role` (control-plane / compute-only / single-box). Without this
  drop-in vmmd falls back to `RoleSingleBox` on a multi-host fleet and the
  `[compute_node]` register row writes `role=single-box` — incompatible with
  fsn-2's `RoleComputeOnly` contract (ADR-092).
- `99-faas-routing.conf.j2` — on compute-only boxes, renders
  `faas_vmmd_listen_addr` and `faas_vmmd_target_url` into a managed drop-in.
  The first is the bind address; the second is the routable address written
  during vmmd self-registration. Both are required by the role, so a fresh
  bootstrap cannot regress to a unix-socket row that needs a manual DB patch.

## Side effects

- Creates the `faas` group (socket auth group, ADR-015) and the `faas-vmmd`
  system user (vmmd socket owner). Mode: nologin, no home dir, group
  membership `faas` so the sibling CHOWN in `pkg/wire/ListenOrRecreate`
  groups the socket correctly.
- Ensures `/etc/faas` exists (mode 0750 root:faas).
- Ensures `/srv/fc/parent` exists (ADR-053 mount scratch, mode 0750
  root:faas).
- Loads the optional archive credential through systemd and prepares
  `/var/log/faas/vmmd-archive` for the bounded Firecracker-log eviction
  spool. vmmd exposes archive health as `vmmd_log_archive_*`; the
  `log_archive` role owns the directory permission checks.
- Asserts the vmmd PKI leaves exist at `/etc/faas/tls/vmmd/{server,
  schedd-client, apid-client}.crt` (Tier 1 control-plane mTLS, ADR-052).
  Failing this stat-assert surfaces a missing bootstrap before vmmd
  attempts to start and refuses to bind with a confusing TLS handshake
  error.
- Asserts `/etc/faas/secrets/vmmd/node.key` is 0400 root:root (ADR-053).
  The loader is strict on `0o400 root:root`; the ansible stat-assert is the
  load-bearing tripwire that catches a misconfigured bootstrap BEFORE vmmd
  boots and crashes with a confusing mode error.
