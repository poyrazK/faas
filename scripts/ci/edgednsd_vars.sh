#!/usr/bin/env bash
# edgednsd_vars.sh VARS_FILE
#
# Stages edgednsd's Cloudflare DNS token (ADR-20261102081618 §3), which CD
# holds as the EDGEDNSD_CF_TOKEN environment secret, for the edgednsd_service
# role. When the secret is set it appends faas_edgednsd_cf_token to VARS_FILE
# (created when missing), and the role installs it as the unit's credential.
# Without the secret nothing changes; a release whose manifest turns edgednsd
# on then fails the control-plane converge at the role's check.
#
# The token is a secret, so it is not kept in the environment's Ansible
# variables (CP_ANSIBLE_VARS_B64); with the secret set, those variables must
# not set faas_edgednsd_cf_token or faas_edgednsd_cf_token_src.
set -euo pipefail

[[ $# -eq 1 ]] || {
  echo "usage: edgednsd_vars.sh VARS_FILE" >&2
  exit 2
}
vars=$1
token=${EDGEDNSD_CF_TOKEN:-}
# A pasted secret often keeps its trailing newline.
token=${token//[$'\r\n\t ']/}
[[ -n "$token" ]] || exit 0

if ! [[ "$token" =~ ^[A-Za-z0-9_-]{32,}$ ]]; then
  echo "::error::EDGEDNSD_CF_TOKEN is not a Cloudflare API token (32 or more letters, digits, - and _)" >&2
  exit 1
fi
if [[ -f "$vars" ]] && grep -qE '^faas_edgednsd_cf_token(_src)?:' "$vars"; then
  echo "::error::$vars sets edgednsd's token; CD stages it from the EDGEDNSD_CF_TOKEN secret, so remove it from the environment's Ansible variables" >&2
  exit 1
fi

umask 077
{
  printf '\n# edgednsd DNS token staged by CD from the EDGEDNSD_CF_TOKEN secret (scripts/ci/edgednsd_vars.sh).\n'
  printf 'faas_edgednsd_cf_token: "%s"\n' "$token"
} >> "$vars"
chmod 0600 "$vars"
