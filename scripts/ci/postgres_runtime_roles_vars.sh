#!/usr/bin/env bash
# postgres_runtime_roles_vars.sh VARS_FILE
#
# Stages the control plane's PostgreSQL runtime-role switches
# (ADR-20261023104734) from CD environment variables:
#
# - POSTGRES_RUNTIME_ROLES_ENABLED=true (step 1) appends
#   faas_postgres_runtime_roles_enabled: true, so control_plane_service and
#   postgres_shared_runtime_role move every control-plane daemon onto its
#   faas_<daemon> role (after proving each peer login).
# - POSTGRES_OWNER_CONTRACT_ENABLED=true (step 2) also appends
#   faas_postgres_owner_contracted: true, so only root can log in as the
#   owner. It requires step 1 in the same environment; the postgres role
#   refuses it while any daemon or compute node still logs in as `faas`.
#
# Unset or "false" changes nothing. Rolling back is setting a variable to
# "false": the roles then put the owner logins and FAAS_DB_ROLE drop-ins back.
set -euo pipefail

[[ $# -eq 1 ]] || {
  echo "usage: postgres_runtime_roles_vars.sh VARS_FILE" >&2
  exit 2
}
vars=$1

# switch NAME: prints true or false, or fails on anything else.
switch() {
  local name=$1 value=${!1:-}
  case "$value" in
    "" | false) echo false ;;
    true) echo true ;;
    *)
      echo "::error::$name must be true or false, not \"$value\"" >&2
      return 1
      ;;
  esac
}
enabled=$(switch POSTGRES_RUNTIME_ROLES_ENABLED)
contracted=$(switch POSTGRES_OWNER_CONTRACT_ENABLED)
if [[ "$contracted" == true && "$enabled" != true ]]; then
  echo "::error::POSTGRES_OWNER_CONTRACT_ENABLED needs POSTGRES_RUNTIME_ROLES_ENABLED=true: daemons still on the owner would be locked out" >&2
  exit 1
fi
[[ "$enabled" == true ]] || exit 0

for key in faas_postgres_runtime_roles_enabled faas_postgres_owner_contracted; do
  if [[ -f "$vars" ]] && grep -qE "^$key:" "$vars"; then
    echo "::error::$vars sets $key; CD stages it from the POSTGRES_RUNTIME_ROLES_ENABLED and POSTGRES_OWNER_CONTRACT_ENABLED variables, so remove it from the environment's Ansible variables" >&2
    exit 1
  fi
done

umask 077
{
  printf '\n# PostgreSQL runtime roles staged by CD (scripts/ci/postgres_runtime_roles_vars.sh).\n'
  printf 'faas_postgres_runtime_roles_enabled: true\n'
  if [[ "$contracted" == true ]]; then
    printf 'faas_postgres_owner_contracted: true\n'
  fi
} >> "$vars"
chmod 0600 "$vars"
