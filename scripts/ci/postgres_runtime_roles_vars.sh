#!/usr/bin/env bash
# postgres_runtime_roles_vars.sh VARS_FILE
#
# Turns on the control plane's PostgreSQL runtime roles (ADR-20261023104734
# step 1) from CD. When the environment variable
# POSTGRES_RUNTIME_ROLES_ENABLED is "true" it appends
# faas_postgres_runtime_roles_enabled: true to VARS_FILE, so the
# control_plane_service role moves apid, schedd, meterd, outboundd and
# bridged onto their faas_<daemon> roles (after proving each peer login).
# Unset or "false" changes nothing. Rolling back is setting it to "false":
# the role then removes the FAAS_DB_ROLE drop-ins.
set -euo pipefail

[[ $# -eq 1 ]] || {
  echo "usage: postgres_runtime_roles_vars.sh VARS_FILE" >&2
  exit 2
}
vars=$1
enabled=${POSTGRES_RUNTIME_ROLES_ENABLED:-}
case "$enabled" in
  "" | false) exit 0 ;;
  true) ;;
  *)
    echo "::error::POSTGRES_RUNTIME_ROLES_ENABLED must be true or false, not \"$enabled\"" >&2
    exit 1
    ;;
esac
if [[ -f "$vars" ]] && grep -qE '^faas_postgres_runtime_roles_enabled:' "$vars"; then
  echo "::error::$vars sets faas_postgres_runtime_roles_enabled; CD stages it from the POSTGRES_RUNTIME_ROLES_ENABLED variable, so remove it from the environment's Ansible variables" >&2
  exit 1
fi

umask 077
{
  printf '\n# PostgreSQL runtime roles staged by CD from POSTGRES_RUNTIME_ROLES_ENABLED (scripts/ci/postgres_runtime_roles_vars.sh).\n'
  printf 'faas_postgres_runtime_roles_enabled: true\n'
} >> "$vars"
chmod 0600 "$vars"
