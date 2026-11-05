#!/usr/bin/env bash
# faas_environment_vars.sh VARS_FILE
#
# Stages faas_environment for the control_plane_service role from
# DEPLOY_ENVIRONMENT: "production" for a production environment (production,
# production-us, ...), so apid runs with FAAS_ENVIRONMENT=production and
# closes object storage provisioning while the safety policy sells plans less
# than they promise (ADR-20261101214152). Without this no deploy set the
# variable and the check never ran in production. An environment's Ansible
# variables that already set faas_environment win.
set -euo pipefail

[[ $# -eq 1 ]] || {
  echo "usage: faas_environment_vars.sh VARS_FILE" >&2
  exit 2
}
vars=$1
case "${DEPLOY_ENVIRONMENT:-}" in
  production | production-*) ;;
  *) exit 0 ;;
esac
if [[ -f "$vars" ]] && grep -qE '^faas_environment:' "$vars"; then
  exit 0
fi
printf 'faas_environment: production\n' >> "$vars"
