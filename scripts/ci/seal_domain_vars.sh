#!/usr/bin/env bash
# seal_domain_vars.sh ROLE OUT_DIR VARS_FILE
# seal_domain_vars.sh --identities ROLE
#
# Stages the seal-domain keys CD holds (ADR 20261022111812) for the
# seal_domains Ansible role: for each domain whose recipient is set, it
# writes the files the hosts of ROLE need into OUT_DIR and appends their
# *_src variables, with faas_seal_domains_managed: true, to VARS_FILE
# (created when missing).
#
#   control-plane  identities of the domains read there (outbound, source,
#                  alerting, storage, control, delivery, account)
#                  and the recipient of every domain (apid seals to it)
#   compute        identities of the domains read there (runtime, build, edge,
#                  edge_tls, control, delivery), and storage's when the S3 gateway runs
#                  on the compute nodes (SEAL_DOMAIN_STORAGE_ON_COMPUTE=true,
#                  ADR-20261031122757)
#
# --identities ROLE prints, one per line, the domains whose private identity
# ROLE's hosts get, so CD fetches only those from the key escrow.
#
# The edge-tls domain is edge_tls here, as in its variables and secrets.
#
# Where the private identities come from (SEAL_DOMAIN_KEY_SOURCE):
#
#   secrets (default)  SEAL_DOMAIN_<DOMAIN>_AGE_KEY GitHub environment secrets,
#                      next to SEAL_DOMAIN_<DOMAIN>_AGE_RECIPIENT; set both or
#                      neither.
#   escrow             <domain>.age files in SEAL_DOMAIN_ESCROW_DIR, which CD
#                      fetches from the control plane's root-only key escrow
#                      (scripts/ci/key_escrow.sh, ADR-20261110210000 §2).
#                      GitHub holds only the recipients, and the script
#                      refuses to run while any SEAL_DOMAIN_<DOMAIN>_AGE_KEY or
#                      FLEET_AGE_KEY secret is still set.
#
# Write modes and readers_verified are not secrets: they stay in the
# environment's Ansible variables (CP_ANSIBLE_VARS_B64,
# COMPUTE_ANSIBLE_VARS_B64), which must not set the variables written here.
set -euo pipefail

usage() {
  echo "usage: seal_domain_vars.sh control-plane|compute OUT_DIR VARS_FILE" >&2
  echo "       seal_domain_vars.sh --identities control-plane|compute" >&2
  exit 2
}
list_only=false
if [[ $# -eq 2 && "$1" == --identities ]]; then
  list_only=true role=$2 out="" vars=""
elif [[ $# -eq 3 ]]; then
  role=$1 out=$2 vars=$3
else
  usage
fi
case "$role" in control-plane | compute) ;; *) usage ;; esac

domains=(outbound source alerting storage control delivery account runtime build edge edge_tls)

source_kind=${SEAL_DOMAIN_KEY_SOURCE:-secrets}
case "$source_kind" in
  secrets) ;;
  escrow)
    leaked=""
    for domain in "${domains[@]}"; do
      upper=$(printf '%s' "$domain" | tr '[:lower:]' '[:upper:]')
      key_var="SEAL_DOMAIN_${upper}_AGE_KEY"
      [[ -z "${!key_var:-}" ]] || leaked+=" $key_var"
    done
    [[ -z "${FLEET_AGE_KEY:-}" ]] || leaked+=" FLEET_AGE_KEY"
    if [[ -n "$leaked" ]]; then
      echo "::error::SEAL_DOMAIN_KEY_SOURCE=escrow, but these private identities are still GitHub secrets:$leaked. Delete them from the environment (the control plane's key escrow holds them) and rotate them (docs/runbooks/seal-domain-key-escrow.md)" >&2
      exit 1
    fi
    if ! $list_only && [[ ! -d "${SEAL_DOMAIN_ESCROW_DIR:-}" ]]; then
      echo "::error::SEAL_DOMAIN_KEY_SOURCE=escrow needs SEAL_DOMAIN_ESCROW_DIR, the identities fetched from the control plane's key escrow" >&2
      exit 1
    fi
    ;;
  *)
    echo "::error::SEAL_DOMAIN_KEY_SOURCE is '$source_kind'; want secrets or escrow" >&2
    exit 1
    ;;
esac

# What ROLE's hosts get of DOMAIN: both, identity, recipient or nothing.
staged() { # ROLE DOMAIN
  case "$1/$2" in
    control-plane/outbound | control-plane/source | control-plane/alerting | control-plane/storage | control-plane/delivery)
      echo both ;;
    control-plane/runtime | control-plane/build | control-plane/edge | control-plane/edge_tls)
      echo recipient ;;
    compute/runtime | compute/build | compute/edge | compute/edge_tls | compute/control | compute/delivery | control-plane/control | control-plane/account)
      echo identity ;;
    compute/storage)
      if [[ "${SEAL_DOMAIN_STORAGE_ON_COMPUTE:-}" == true ]]; then echo identity; fi ;;
  esac
}

if ! $list_only && [[ -f "$vars" ]] && grep -qE '^(faas_seal_domains_managed|faas_seal_domain_[a-z_]+_(identity|recipient)_src):' "$vars"; then
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
  elif [[ "$kind" == previous_identity ]]; then
    file="$out/$domain.age.previous" pattern='^AGE-SECRET-KEY-1[0-9A-Z]+$' mode=0400
  else
    file="$out/$domain.age.pub" pattern='^age1[0-9a-z]+$' mode=0444
  fi
  if ! [[ "$value" =~ $pattern ]]; then
    echo "::error::the $domain seal-domain $kind ($source_kind) is not an age $kind" >&2
    exit 1
  fi
  install -d -m 0700 "$out"
  # Byte-exact: the strict age parser rejects a trailing newline.
  printf '%s' "$value" > "$file"
  chmod "$mode" "$file"
  added+="faas_seal_domain_${domain}_${kind}_src: \"$file\""$'\n'
}

# identity_of DOMAIN KEY_VAR sets $identity to DOMAIN's private identity,
# from the secret or from the fetched escrow. It runs in this shell, not a
# command substitution, so a missing escrow file stops the script.
identity=""
identity_of() {
  if [[ "$source_kind" == secrets ]]; then
    identity=${!2}
    return
  fi
  local file="$SEAL_DOMAIN_ESCROW_DIR/$1.age"
  if [[ ! -s "$file" ]]; then
    echo "::error::the control plane's key escrow holds no $1 identity; escrow it first (docs/runbooks/seal-domain-key-escrow.md)" >&2
    exit 1
  fi
  identity=$(<"$file")
}

# During an escrowed rotation (key_escrow.sh rotate) the old identity sits
# beside the new one as <domain>.age.previous; the role keeps it next to the
# new one until the domain is re-sealed (ADR-057 overlap).
stage_previous() { # DOMAIN
  local file="${SEAL_DOMAIN_ESCROW_DIR:-}/$1.age.previous"
  if [[ "$source_kind" == escrow && -s "$file" ]]; then
    stage previous_identity "$1" "$(<"$file")"
  fi
}

for domain in "${domains[@]}"; do
  upper=$(printf '%s' "$domain" | tr '[:lower:]' '[:upper:]')
  key_var="SEAL_DOMAIN_${upper}_AGE_KEY"
  pub_var="SEAL_DOMAIN_${upper}_AGE_RECIPIENT"
  key=${!key_var:-}
  pub=${!pub_var:-}
  if [[ -z "$key" && -z "$pub" ]]; then
    continue
  fi
  if [[ "$source_kind" == secrets && ( -z "$key" || -z "$pub" ) ]]; then
    echo "::error::set both $key_var and $pub_var, or neither" >&2
    exit 1
  fi
  what=$(staged "$role" "$domain")
  if $list_only; then
    if [[ "$what" == both || "$what" == identity ]]; then echo "$domain"; fi
    continue
  fi
  case "$what" in
    both)
      identity_of "$domain" "$key_var"
      stage identity "$domain" "$identity"
      stage_previous "$domain"
      stage recipient "$domain" "$pub"
      ;;
    recipient)
      stage recipient "$domain" "$pub"
      ;;
    identity)
      identity_of "$domain" "$key_var"
      stage identity "$domain" "$identity"
      stage_previous "$domain"
      ;;
  esac
  identity=""
  recipients+="# $domain recipient: $pub"$'\n'
done
if $list_only; then
  exit 0
fi

if [[ -n "$added" ]]; then
  {
    # The secrets line is unchanged from before the escrow, so a host's
    # contract hash moves only when its key source does.
    if [[ "$source_kind" == secrets ]]; then
      printf '\n# Seal-domain keys staged by CD from the SEAL_DOMAIN_* secrets (scripts/ci/seal_domain_vars.sh).\n'
    else
      printf '\n# Seal-domain keys staged by CD from the control-plane key escrow (scripts/ci/seal_domain_vars.sh).\n'
    fi
    printf '%s' "$recipients"
    printf 'faas_seal_domains_managed: true\n'
    printf '%s' "$added"
  } >> "$vars"
  chmod 0600 "$vars"
fi
