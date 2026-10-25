#!/usr/bin/env bash
# Hermetic tests for seal_domain_vars.sh (ADR 20261022111812).
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$repo_root/scripts/ci/seal_domain_vars.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

key() { printf 'AGE-SECRET-KEY-1%s' "$(printf '%s' "$1" | tr '[:lower:]' '[:upper:]')"; }
pub() { printf 'age1%s' "$1"; }
run() { # ROLE CASE [VAR=VALUE...]; prints the vars file
  local role=$1 case=$2
  shift 2
  env -i PATH="$PATH" "$@" bash "$script" "$role" "$work/$case/out" "$work/$case/vars.yml"
}

# Nothing set: the vars file is left alone.
run control-plane none || fail "no secrets failed"
[[ ! -e "$work/none/vars.yml" ]] || fail "no secrets still wrote a vars file"

# Control plane: identities of outbound and source, recipients of everything.
all=(SEAL_DOMAIN_OUTBOUND_AGE_KEY="$(key outbound)" SEAL_DOMAIN_OUTBOUND_AGE_RECIPIENT="$(pub outbound)"
  SEAL_DOMAIN_SOURCE_AGE_KEY="$(key source)" SEAL_DOMAIN_SOURCE_AGE_RECIPIENT="$(pub source)"
  SEAL_DOMAIN_RUNTIME_AGE_KEY="$(key runtime)" SEAL_DOMAIN_RUNTIME_AGE_RECIPIENT="$(pub runtime)"
  SEAL_DOMAIN_BUILD_AGE_KEY="$(key build)" SEAL_DOMAIN_BUILD_AGE_RECIPIENT="$(pub build)"
  SEAL_DOMAIN_EDGE_AGE_KEY="$(key edge)" SEAL_DOMAIN_EDGE_AGE_RECIPIENT="$(pub edge)")
mkdir -p "$work/cp"
printf 'faas_box_role: control-plane\n' > "$work/cp/vars.yml"
run control-plane cp "${all[@]}" || fail "control-plane staging failed"
vars="$(cat "$work/cp/vars.yml")"
[[ "$vars" == "faas_box_role: control-plane"* ]] || fail "existing variables were not kept: $vars"
for want in "faas_seal_domains_managed: true" \
  "faas_seal_domain_outbound_identity_src: \"$work/cp/out/outbound.age\"" \
  "faas_seal_domain_outbound_recipient_src: \"$work/cp/out/outbound.age.pub\"" \
  "faas_seal_domain_source_identity_src: \"$work/cp/out/source.age\"" \
  "faas_seal_domain_runtime_recipient_src: \"$work/cp/out/runtime.age.pub\"" \
  "faas_seal_domain_build_recipient_src: \"$work/cp/out/build.age.pub\"" \
  "faas_seal_domain_edge_recipient_src: \"$work/cp/out/edge.age.pub\""; do
  grep -qxF "$want" "$work/cp/vars.yml" || fail "control plane is missing: $want"$'\n'"$vars"
done
grep -qxF "# runtime recipient: $(pub runtime)" "$work/cp/vars.yml" || fail "the runtime recipient is not recorded for the contract hash"
for unwanted in runtime_identity_src build_identity_src edge_identity_src; do
  ! grep -q "$unwanted" "$work/cp/vars.yml" || fail "control plane got $unwanted"
done
[[ ! -e "$work/cp/out/runtime.age" && ! -e "$work/cp/out/build.age" && ! -e "$work/cp/out/edge.age" ]] || fail "compute identities were written for the control plane"
[[ "$(cat "$work/cp/out/outbound.age")" == "$(key outbound)" ]] || fail "identity is not byte-exact"
[[ "$(wc -c < "$work/cp/out/outbound.age" | tr -d ' ')" == "$(printf '%s' "$(key outbound)" | wc -c | tr -d ' ')" ]] || fail "identity has a trailing newline"

# Compute: only the identities read there.
run compute compute "${all[@]}" || fail "compute staging failed"
for want in "faas_seal_domain_runtime_identity_src: \"$work/compute/out/runtime.age\"" \
  "faas_seal_domain_build_identity_src: \"$work/compute/out/build.age\"" \
  "faas_seal_domain_edge_identity_src: \"$work/compute/out/edge.age\""; do
  grep -qxF "$want" "$work/compute/vars.yml" || fail "compute is missing: $want"
done
! grep -vE '^#' "$work/compute/vars.yml" | grep -qE 'outbound|source|recipient_src' || fail "compute got control-plane keys: $(cat "$work/compute/vars.yml")"

# Half a pair, a malformed key, and variables CD owns all refuse.
if run compute half SEAL_DOMAIN_RUNTIME_AGE_KEY="$(key runtime)" 2>/dev/null; then fail "half a pair was accepted"; fi
if run compute bad SEAL_DOMAIN_RUNTIME_AGE_KEY="not-a-key" SEAL_DOMAIN_RUNTIME_AGE_RECIPIENT="$(pub runtime)" 2>/dev/null; then
  fail "a malformed identity was accepted"
fi
mkdir -p "$work/owned"
printf 'faas_seal_domain_runtime_identity_src: /tmp/x\n' > "$work/owned/vars.yml"
if run compute owned SEAL_DOMAIN_RUNTIME_AGE_KEY="$(key runtime)" SEAL_DOMAIN_RUNTIME_AGE_RECIPIENT="$(pub runtime)" 2>/dev/null; then
  fail "a vars file that already sets a key variable was accepted"
fi
if bash "$script" edge "$work/x" "$work/x.yml" 2>/dev/null; then fail "an unknown role was accepted"; fi

echo "seal_domain_vars: ok"
