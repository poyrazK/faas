#!/usr/bin/env bash
# edge_challenge_key_vars.sh VARS_FILE
#
# Stages the fleet's browser-challenge signing key (ADR-831 amendment 17),
# which CD holds as the GATEWAYD_EDGE_CHALLENGE_KEY environment secret, for
# the gatewayd_internal_service role. When the secret is set it appends
# gatewayd_edge_challenge_key and gatewayd_edge_challenge_key_required: true
# to VARS_FILE (created when missing), so every gateway the environment
# deploys signs challenges with the same key, and a gateway host that would
# get none fails the play. The key is the gateway fleet key
# (ADR-20261102191500): it also signs managed deadlines, and a deploy without
# the secret fails here.
#
# The key is a secret, so it is not kept in the environment's Ansible
# variables (COMPUTE_ANSIBLE_VARS_B64); with the secret set, those variables
# must not set either variable written here.
set -euo pipefail

[[ $# -eq 1 ]] || {
  echo "usage: edge_challenge_key_vars.sh VARS_FILE" >&2
  exit 2
}
vars=$1
key=${GATEWAYD_EDGE_CHALLENGE_KEY:-}
if [[ -z "$key" ]]; then
  echo "::error::GATEWAYD_EDGE_CHALLENGE_KEY is not set for this environment; every gateway needs the shared gateway fleet key (ADR-20261102191500). Create it: openssl rand -hex 32 | gh secret set GATEWAYD_EDGE_CHALLENGE_KEY --env <environment>" >&2
  exit 1
fi

if ! [[ "$key" =~ ^[0-9a-fA-F]{64}$ ]]; then
  echo "::error::GATEWAYD_EDGE_CHALLENGE_KEY must be 64 hex digits (openssl rand -hex 32)" >&2
  exit 1
fi
if [[ -f "$vars" ]] && grep -qE '^gatewayd_edge_challenge_key(_required)?:' "$vars"; then
  echo "::error::$vars sets gatewayd_edge_challenge_key; CD stages it from the GATEWAYD_EDGE_CHALLENGE_KEY secret, so remove it from the environment's Ansible variables" >&2
  exit 1
fi

umask 077
{
  printf '\n# Browser-challenge signing key staged by CD from the GATEWAYD_EDGE_CHALLENGE_KEY secret (scripts/ci/edge_challenge_key_vars.sh).\n'
  printf 'gatewayd_edge_challenge_key: "%s"\n' "$(printf '%s' "$key" | tr 'A-F' 'a-f')"
  printf 'gatewayd_edge_challenge_key_required: true\n'
} >> "$vars"
chmod 0600 "$vars"
