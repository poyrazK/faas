#!/usr/bin/env bash
# faas_environment_vars.sh VARS_FILE
#
# Stages faas_environment for the control_plane_service role from
# DEPLOY_ENVIRONMENT: "production" for a production environment (production,
# production-us, ...), so apid runs with FAAS_ENVIRONMENT=production and
# closes object storage provisioning while the safety policy sells plans less
# than they promise (ADR-20261101214152). Without this no deploy set the
# variable and the check never ran in production. Also stage the specific
# fleet name for Alertmanager pages. Explicit Ansible values take precedence.
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
if ! grep -qsE '^faas_environment:' "$vars"; then
  printf '\nfaas_environment: production\n' >> "$vars"
fi
if [[ "$DEPLOY_ENVIRONMENT" =~ ^production(-[a-z0-9]+)*$ ]] && ! grep -qsE '^am_fleet_name:' "$vars"; then
  printf '\nam_fleet_name: "%s"\n' "$DEPLOY_ENVIRONMENT" >> "$vars"
fi
