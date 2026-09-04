#!/usr/bin/env bash
# Static gate for the committed production systemd units. Live drop-ins are
# checked by the deployment runbook; this gate prevents the source of truth
# from weakening while a test-box diagnostic override is being removed.
set -euo pipefail

root="${1:-.}"
unit_root="${root}/deploy/ansible/roles"
units=(
  "control_plane_service/files/faas-apid.service"
  "control_plane_service/files/faas-schedd.service"
  "control_plane_service/files/faas-meterd.service"
  "gatewayd_public_service/files/faas-gatewayd-public.service"
  "gatewayd_internal_service/files/faas-gatewayd-internal.service"
  "githubd_service/files/faas-githubd.service"
  "builderd_service/files/faas-builderd.service"
  "compute_only_service/files/faas-imaged.service"
  "vmmd_service/files/faas-vmmd.service"
)

required=(NoNewPrivileges=yes ProtectSystem=strict ProtectHome=yes ProtectKernelModules=yes)
errors=0

for rel in "${units[@]}"; do
  file="${unit_root}/${rel}"
  if [[ ! -f "$file" ]]; then
    echo "systemd-hardening-check: missing ${file}" >&2
    errors=$((errors + 1))
    continue
  fi
  user="$(sed -nE 's/^User=(.*)$/\1/p' "$file" | head -1)"
  if [[ "$rel" != vmmd_service/files/faas-vmmd.service && ( -z "$user" || "$user" == root ) ]]; then
    echo "systemd-hardening-check: ${rel}: User must be non-root" >&2
    errors=$((errors + 1))
  fi
  unit_required=("${required[@]}")
  if [[ "$rel" != vmmd_service/files/faas-vmmd.service ]]; then
    unit_required+=(ProtectKernelTunables=yes ProtectControlGroups=yes)
  else
    # vmmd is the deliberate exception: Firecracker's jailer manages the
    # delegated per-VM cgroups and writes their kernel control files. The
    # canonical daemon spec keeps both protections disabled for that owner;
    # requiring them here would make vmmd unable to start/manage guests.
    :
  fi
  for directive in "${unit_required[@]}"; do
    if ! grep -Fqx "$directive" "$file"; then
      echo "systemd-hardening-check: ${rel}: missing ${directive}" >&2
      errors=$((errors + 1))
    fi
  done
  # Every daemon must bound its own memory. The value is per-daemon (256M
  # for the small control-plane services, 4G for imaged's layer work), so
  # this is a presence check rather than an exact-line match.
  #
  # vmmd regressed here: it was the only unit without a cap, apparently
  # dropped when Delegate=yes was added out of concern the cap would also
  # bound the delegated children. It does not — firecracker VMs live under
  # faas-tenant.slice, not vmmd's cgroup. On 2026-09-03 an uncapped vmmd
  # reached 2.1 GB RSS and the shared 3 GB faas-cp.slice OOM-killed it,
  # taking the compute node out of rotation. This check is the tripwire.
  if ! grep -Eq '^MemoryMax=' "$file"; then
    echo "systemd-hardening-check: ${rel}: missing MemoryMax= (an unbounded daemon can OOM its whole slice)" >&2
    errors=$((errors + 1))
  fi
  # vmmd and schedd deliberately share the host mount namespace so their
  # /run/faas sockets remain visible across daemon namespaces. All other
  # production daemons must have a private temporary directory.
  if [[ "$rel" != vmmd_service/files/faas-vmmd.service && "$rel" != control_plane_service/files/faas-schedd.service ]] && ! grep -Fqx 'PrivateTmp=yes' "$file"; then
    echo "systemd-hardening-check: ${rel}: missing PrivateTmp=yes" >&2
    errors=$((errors + 1))
  fi
  # vmmd is the root component and writes its explicitly-whitelisted secret
  # paths under /etc/faas; its unit documents that exception.
  if [[ "$rel" != vmmd_service/files/faas-vmmd.service ]] && ! grep -Fqx 'ReadOnlyPaths=/etc/faas' "$file"; then
    echo "systemd-hardening-check: ${rel}: missing ReadOnlyPaths=/etc/faas" >&2
    errors=$((errors + 1))
  fi
  if grep -Eiq '(^|[[:space:]])(FAAS_[A-Z0-9_]*DEBUG|FAAS_[A-Z0-9_]*DIAGNOSTIC|GODEBUG|LOG_LEVEL=debug)=' "$file"; then
    echo "systemd-hardening-check: ${rel}: diagnostic/debug environment is not release-safe" >&2
    errors=$((errors + 1))
  fi
done

if (( errors > 0 )); then
  echo "systemd-hardening-check: FAIL (${errors} finding(s))" >&2
  exit 1
fi
echo "systemd-hardening-check: OK (${#units[@]} production units)"
