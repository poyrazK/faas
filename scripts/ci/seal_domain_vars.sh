#!/usr/bin/env bash
# seal_domain_vars.sh ROLE OUT_DIR VARS_FILE
#
# Stages the seal-domain keys CD holds as GitHub environment secrets (ADR
# 20261022111812) for the seal_domains Ansible role: for each domain whose
# secrets are set, it writes the files the hosts of ROLE need into OUT_DIR
# and appends their *_src variables, with faas_seal_domains_managed: true, to
# VARS_FILE (created when missing).
#
#   control-plane  identities of the domains read there (outbound, source)
#                  and the recipient of every domain (apid seals to it)
#   compute        identities of the domains read there (runtime, build)
#
# Secrets, per domain: SEAL_DOMAIN_<DOMAIN>_AGE_KEY and
# SEAL_DOMAIN_<DOMAIN>_AGE_RECIPIENT, from `gregalectl seal-domain init`. Set
# both or neither. Write modes and readers_verified are not secrets: they
# stay in the environment's Ansible variables (CP_ANSIBLE_VARS_B64,
# COMPUTE_ANSIBLE_VARS_B64), which must not set the variables written here.
set -euo pipefail

usage() {
  echo "usage: seal_domain_vars.sh control-plane|compute OUT_DIR VARS_FILE" >&2
  exit 2
}
[[ $# -eq 3 ]] || usage
role=$1 out=$2 vars=$3
case "$role" in control-plane | compute) ;; *) usage ;; esac

if [[ -f "$vars" ]] && grep -qE '^(faas_seal_domains_managed|faas_seal_domain_[a-z]+_(identity|recipient)_src):' "$vars"; then
  echo "::error::$vars sets seal-domain key variables; CD stages them from the SEAL_DOMAIN_* secrets, so remove them from the environment's Ansible variables" >&2
  exit 1
fi

added=""
# Public recipients, as comments: a rotated key changes the vars file, so
# converge-control-plane's contract hash sees it.
recipients=""
stage() { # KIND DOMAIN VALUE
  local kind=$1 domain=$2 value=$3 file pattern mode
  if [[ "$kind" == identity ]]; then
    file="$out/$domain.age" pattern='^AGE-SECRET-KEY-1[0-9A-Z]+$' mode=0400
  else
    file="$out/$domain.age.pub" pattern='^age1[0-9a-z]+$' mode=0444
  fi
  if ! [[ "$value" =~ $pattern ]]; then
    echo "::error::the $domain seal-domain $kind secret is not an age $kind" >&2
    exit 1
  fi
  install -d -m 0700 "$out"
  # Byte-exact: the strict age parser rejects a trailing newline.
  printf '%s' "$value" > "$file"
  chmod "$mode" "$file"
  added+="faas_seal_domain_${domain}_${kind}_src: \"$file\""$'\n'
}

for domain in outbound source runtime build; do
  upper=$(printf '%s' "$domain" | tr '[:lower:]' '[:upper:]')
  key_var="SEAL_DOMAIN_${upper}_AGE_KEY"
  pub_var="SEAL_DOMAIN_${upper}_AGE_RECIPIENT"
  key=${!key_var:-}
  pub=${!pub_var:-}
  if [[ -z "$key" && -z "$pub" ]]; then
    continue
  fi
  if [[ -z "$key" || -z "$pub" ]]; then
    echo "::error::set both $key_var and $pub_var, or neither" >&2
    exit 1
  fi
  case "$role/$domain" in
    control-plane/outbound | control-plane/source)
      stage identity "$domain" "$key"
      stage recipient "$domain" "$pub"
      ;;
    control-plane/runtime | control-plane/build)
      stage recipient "$domain" "$pub"
      ;;
    compute/runtime | compute/build)
      stage identity "$domain" "$key"
      ;;
  esac
  recipients+="# $domain recipient: $pub"$'\n'
done

if [[ -n "$added" ]]; then
  {
    printf '\n# Seal-domain keys staged by CD from the SEAL_DOMAIN_* secrets (scripts/ci/seal_domain_vars.sh).\n'
    printf '%s' "$recipients"
    printf 'faas_seal_domains_managed: true\n'
    printf '%s' "$added"
  } >> "$vars"
  chmod 0600 "$vars"
fi
