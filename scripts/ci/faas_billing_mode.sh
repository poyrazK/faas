#!/usr/bin/env bash
# faas_billing_mode.sh resolve
# faas_billing_mode.sh vars VARS_FILE
#
# The control plane's billing mode (FAAS_BILLING_MODE for apid and meterd) is
# a per-environment deploy setting: the GitHub environment variable
# FAAS_BILLING_MODE, passed to this script as FAAS_BILLING_MODE_SETTING. It is
# "disabled" (the public-beta default) or "live"; unset means disabled.
#
#   resolve           print the validated mode (the signed bundle writes it)
#   vars VARS_FILE    stage faas_billing_mode for the control_plane_service
#                     role, so Ansible renders the same drop-in as the bundle
#                     (TestAnsibleAndBundleRenderTheSameControlPlaneDropIns).
#                     Vars that already set a different value fail the deploy:
#                     two renders would restart apid on every release.
set -euo pipefail

mode=${FAAS_BILLING_MODE_SETTING:-disabled}
case "$mode" in
  disabled | live) ;;
  *)
    echo "FAAS_BILLING_MODE must be disabled or live (got \"$mode\")" >&2
    exit 1
    ;;
esac

case "${1:-}" in
  resolve)
    [[ $# -eq 1 ]] || {
      echo "usage: faas_billing_mode.sh resolve" >&2
      exit 2
    }
    printf '%s\n' "$mode"
    ;;
  vars)
    [[ $# -eq 2 ]] || {
      echo "usage: faas_billing_mode.sh vars VARS_FILE" >&2
      exit 2
    }
    vars=$2
    if [[ -f "$vars" ]] && grep -qE '^faas_billing_mode:' "$vars"; then
      current=$(sed -n -E "s/^faas_billing_mode:[[:space:]]*[\"']?([a-z]+)[\"']?[[:space:]]*$/\\1/p" "$vars" | tail -n 1)
      if [[ "$current" != "$mode" ]]; then
        echo "Ansible vars set faas_billing_mode: $current but the environment's FAAS_BILLING_MODE is $mode; remove it from the vars" >&2
        exit 1
      fi
      exit 0
    fi
    printf 'faas_billing_mode: %s\n' "$mode" >>"$vars"
    ;;
  *)
    echo "usage: faas_billing_mode.sh resolve | vars VARS_FILE" >&2
    exit 2
    ;;
esac
