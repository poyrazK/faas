#!/usr/bin/env bash
# provision-safe-release-tokens.sh SEALED_ENV METERD_ENV
#
# ADR-1189: give a control plane the Safe Deploy service-token pair
# (FAAS_CANARY_PROGRESSION_TOKEN, FAAS_SAFEDEPLOY_TOKEN) so canary progression,
# alert rollbacks and stuck-canary recovery run without hand provisioning.
# APID reads the pair from SEALED_ENV and meterd from METERD_ENV; both must
# hold the same values.
#
# Existing values are never replaced. A token present in only one file is
# copied to the other; a missing token is generated (32 random bytes, hex).
# Files keep their mode and owner: values are appended. Values are never
# printed. The last line is "changed=0" or "changed=1".
set -euo pipefail

sealed="${1:?usage: provision-safe-release-tokens.sh SEALED_ENV METERD_ENV}"
meterd="${2:?usage: provision-safe-release-tokens.sh SEALED_ENV METERD_ENV}"

for file in "$sealed" "$meterd"; do
  if [ ! -f "$file" ]; then
    echo "FAIL: $file is missing; provision it before running this role" >&2
    exit 1
  fi
done

# value FILE NAME prints NAME's last value in FILE, or nothing.
value() {
  grep -E "^$2=" "$1" 2>/dev/null | tail -n 1 | cut -d= -f2- || true
}

# append FILE NAME VALUE adds NAME=VALUE on its own line.
append() {
  if [ -s "$1" ] && [ -n "$(tail -c 1 "$1")" ]; then
    printf '\n' >>"$1"
  fi
  printf '%s=%s\n' "$2" "$3" >>"$1"
}

changed=0
for name in FAAS_CANARY_PROGRESSION_TOKEN FAAS_SAFEDEPLOY_TOKEN; do
  in_sealed="$(value "$sealed" "$name")"
  in_meterd="$(value "$meterd" "$name")"
  if [ -n "$in_sealed" ] && [ -n "$in_meterd" ]; then
    if [ "$in_sealed" != "$in_meterd" ]; then
      echo "FAIL: $name differs between $sealed and $meterd; set the same value in both" >&2
      exit 1
    fi
    continue
  fi
  token="${in_sealed:-$in_meterd}"
  if [ -z "$token" ]; then
    token="$(openssl rand -hex 32)"
  fi
  [ -n "$in_sealed" ] || append "$sealed" "$name" "$token"
  [ -n "$in_meterd" ] || append "$meterd" "$name" "$token"
  changed=1
done

if [ "$(value "$sealed" FAAS_CANARY_PROGRESSION_TOKEN)" = "$(value "$sealed" FAAS_SAFEDEPLOY_TOKEN)" ]; then
  echo "FAIL: FAAS_CANARY_PROGRESSION_TOKEN and FAAS_SAFEDEPLOY_TOKEN must differ" >&2
  exit 1
fi
echo "changed=$changed"
