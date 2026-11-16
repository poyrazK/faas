#!/usr/bin/env bash
# key_escrow.sh — the control plane's root-only escrow of private age
# identities (ADR-20261110210000 §2).
#
#   key_escrow.sh put NAME...           escrow each NAME's identity from its
#                                       GitHub secret (FLEET_AGE_KEY for fleet,
#                                       SEAL_DOMAIN_<NAME>_AGE_KEY otherwise)
#   key_escrow.sh fetch OUT_DIR NAME... copy the escrowed identities into
#                                       OUT_DIR/<name>.age (0400) for one rollout
#                                       (and <name>.age.previous while a
#                                       rotation is in progress)
#   key_escrow.sh status NAME...        print "<name> present|missing", plus
#                                       "previous" while a rotation runs
#   key_escrow.sh rotate DOMAIN         on the control plane, keep the current
#                                       identity as <domain>.age.previous and
#                                       generate a new one with gregalectl;
#                                       prints only the new recipient
#   key_escrow.sh retire DOMAIN         remove <domain>.age.previous once the
#                                       domain is re-sealed to the new identity
#
# NAME is fleet or a seal domain (outbound source alerting storage control
# delivery account runtime build edge edge_tls).
#
# The escrow is KEY_ESCROW_DIR (default /etc/faas/secrets/key-escrow) on
# KEY_ESCROW_HOST, a control-plane host: a 0700 root:root directory of 0400
# files that no daemon loads. CD reads from it instead of GitHub secrets once
# the environment sets SEAL_DOMAIN_KEY_SOURCE=escrow, so GitHub holds only
# public recipients.
#
# SSH: root@KEY_ESCROW_HOST with the key file KEY_ESCROW_SSH_KEY, trusting only
# the host keys in the file KEY_ESCROW_KNOWN_HOSTS. Identities travel on the
# SSH channel's stdin/stdout, never in a command line. KEY_ESCROW_SSH replaces
# the ssh binary (tests).
#
# put never replaces an escrowed identity with a different one: rotation is a
# separate, deliberate step (docs/runbooks/seal-domain-key-escrow.md).
set -euo pipefail

usage() {
  echo "usage: key_escrow.sh put NAME... | fetch OUT_DIR NAME... | status NAME... | rotate DOMAIN | retire DOMAIN" >&2
  exit 2
}
[[ $# -ge 1 ]] || usage
cmd=$1
shift

remote_dir=${KEY_ESCROW_DIR:-/etc/faas/secrets/key-escrow}
identity_pattern='^AGE-SECRET-KEY-1[0-9A-Z]+$'

check_name() {
  case "$1" in
    fleet | outbound | source | alerting | storage | control | delivery | account | runtime | build | edge | edge_tls) ;;
    *)
      echo "::error::key_escrow: '$1' is not fleet or a seal domain" >&2
      exit 2
      ;;
  esac
}

secret_var() { # NAME -> the GitHub secret that holds its identity
  if [[ "$1" == fleet ]]; then
    echo FLEET_AGE_KEY
  else
    echo "SEAL_DOMAIN_$(printf '%s' "$1" | tr '[:lower:]' '[:upper:]')_AGE_KEY"
  fi
}

remote() { # REMOTE_SCRIPT ARG... ; runs REMOTE_SCRIPT under bash on the host
  local host=${KEY_ESCROW_HOST:-} key=${KEY_ESCROW_SSH_KEY:-} known=${KEY_ESCROW_KNOWN_HOSTS:-}
  if [[ -z "$host" || ! -s "$key" || ! -s "$known" ]]; then
    echo "::error::key_escrow: set KEY_ESCROW_HOST and the files KEY_ESCROW_SSH_KEY and KEY_ESCROW_KNOWN_HOSTS" >&2
    exit 1
  fi
  if ! [[ "$host" =~ ^[A-Za-z0-9.:-]+$ ]]; then
    echo "::error::key_escrow: KEY_ESCROW_HOST is not a host name or address" >&2
    exit 1
  fi
  local script=$1
  shift
  local command arg
  command="bash -c $(printf '%q' "$script") key-escrow"
  for arg in "$@"; do
    command+=" $(printf '%q' "$arg")"
  done
  local ssh=(ssh)
  if [[ -n "${KEY_ESCROW_SSH:-}" ]]; then
    ssh=("$KEY_ESCROW_SSH")
  fi
  "${ssh[@]}" -i "$key" -o BatchMode=yes -o StrictHostKeyChecking=yes \
    -o "UserKnownHostsFile=$known" "root@$host" "$command"
}

# Runs on the control-plane host: DIR NAME, identity on stdin.
# shellcheck disable=SC2016 # expands on the control-plane host
put_script='set -euo pipefail
dir=$1 name=$2
umask 077
# Root on the control plane; the hermetic test runs unprivileged.
root=false
[[ $(id -u) -eq 0 ]] && root=true
if $root; then install -d -o root -g root -m 0700 "$dir"; else install -d -m 0700 "$dir"; fi
tmp=$(mktemp "$dir/.$name.XXXXXX")
trap '\''rm -f -- "$tmp"'\'' EXIT
cat > "$tmp"
grep -qE "^AGE-SECRET-KEY-1[0-9A-Z]+\$" "$tmp" || { echo "not an age identity" >&2; exit 3; }
if [[ -e "$dir/$name.age" ]]; then
  if cmp -s "$tmp" "$dir/$name.age"; then echo unchanged; exit 0; fi
  echo "a different identity is already escrowed" >&2
  exit 4
fi
if $root; then chown root:root "$tmp"; fi
chmod 0400 "$tmp"
mv -f -- "$tmp" "$dir/$name.age"
trap - EXIT
echo escrowed'

# Runs on the control-plane host: DIR NAME...; writes a tar of the files.
# shellcheck disable=SC2016
fetch_script='set -euo pipefail
dir=$1
shift
cd -- "$dir"
files=()
for name in "$@"; do
  [[ -s "$name.age" ]] || { echo "missing: $name" >&2; exit 3; }
  files+=("$name.age")
  if [[ -s "$name.age.previous" ]]; then files+=("$name.age.previous"); fi
done
tar -cf - -- "${files[@]}"'

# Runs on the control-plane host: DIR NAME...
# shellcheck disable=SC2016
status_script='set -euo pipefail
dir=$1
shift
for name in "$@"; do
  state=missing
  if [[ -s "$dir/$name.age" ]]; then state=present; fi
  if [[ -s "$dir/$name.age.previous" ]]; then state+=" (previous kept: rotation in progress)"; fi
  echo "$name $state"
done'

# Runs on the control-plane host: DIR NAME GREGALECTL_DOMAIN GREGALECTL.
# The new identity is generated there and never leaves except to CD's fetch.
# shellcheck disable=SC2016
rotate_script='set -euo pipefail
dir=$1 name=$2 domain=$3 gregalectl=$4
umask 077
cd -- "$dir"
[[ -s "$name.age" ]] || { echo "no current $name identity is escrowed" >&2; exit 3; }
[[ ! -e "$name.age.previous" ]] || { echo "a previous $name identity is still escrowed; finish that rotation and retire it first" >&2; exit 4; }
tmp=$(mktemp -d "$dir/.rotate.XXXXXX")
trap '\''rm -rf -- "$tmp"'\'' EXIT
"$gregalectl" seal-domain init --domain "$domain" --dir "$tmp" >/dev/null
grep -qE "^AGE-SECRET-KEY-1[0-9A-Z]+\$" "$tmp/$domain.age" || { echo "gregalectl wrote no age identity" >&2; exit 5; }
grep -qE "^age1[0-9a-z]+\$" "$tmp/$domain.age.pub" || { echo "gregalectl wrote no age recipient" >&2; exit 5; }
chmod 0400 "$tmp/$domain.age"
ln -- "$name.age" "$name.age.previous"
mv -f -- "$tmp/$domain.age" "$name.age"
cat -- "$tmp/$domain.age.pub"'

# Runs on the control-plane host: DIR NAME.
# shellcheck disable=SC2016
retire_script='set -euo pipefail
dir=$1 name=$2
cd -- "$dir"
[[ -e "$name.age.previous" ]] || { echo "no previous $name identity is escrowed"; exit 0; }
chmod u+w "$name.age.previous"
if command -v shred >/dev/null; then shred -u -- "$name.age.previous"; else rm -f -- "$name.age.previous"; fi
echo retired'

# gregalectl names the edge-tls domain with a hyphen.
gregalectl_domain() {
  if [[ "$1" == edge_tls ]]; then echo edge-tls; else echo "$1"; fi
}

case "$cmd" in
  put)
    [[ $# -ge 1 ]] || usage
    status=0
    for name in "$@"; do
      check_name "$name"
      var=$(secret_var "$name")
      value=${!var:-}
      if [[ -z "$value" ]]; then
        echo "$name: skipped ($var is not set)"
        continue
      fi
      if ! [[ "$value" =~ $identity_pattern ]]; then
        echo "::error::key_escrow: $var is not an age identity" >&2
        status=1
        continue
      fi
      if result=$(printf '%s' "$value" | remote "$put_script" "$remote_dir" "$name"); then
        echo "$name: $result"
      else
        echo "::error::key_escrow: $name was not escrowed (a different identity may already be escrowed; see the runbook)" >&2
        status=1
      fi
    done
    exit "$status"
    ;;
  fetch)
    [[ $# -ge 2 ]] || usage
    out=$1
    shift
    for name in "$@"; do check_name "$name"; done
    umask 077
    install -d -m 0700 "$out"
    if ! remote "$fetch_script" "$remote_dir" "$@" | tar -xf - -C "$out"; then
      echo "::error::key_escrow: could not fetch $* from the control plane's key escrow" >&2
      exit 1
    fi
    for name in "$@"; do
      file="$out/$name.age"
      if ! [[ -s "$file" && "$(<"$file")" =~ $identity_pattern ]]; then
        echo "::error::key_escrow: the escrowed $name is not an age identity" >&2
        exit 1
      fi
      chmod 0400 "$file"
      if [[ -e "$file.previous" ]]; then
        if ! [[ -s "$file.previous" && "$(<"$file.previous")" =~ $identity_pattern ]]; then
          echo "::error::key_escrow: the escrowed previous $name is not an age identity" >&2
          exit 1
        fi
        chmod 0400 "$file.previous"
      fi
    done
    ;;
  status)
    [[ $# -ge 1 ]] || usage
    for name in "$@"; do check_name "$name"; done
    remote "$status_script" "$remote_dir" "$@"
    ;;
  rotate)
    [[ $# -eq 1 ]] || usage
    check_name "$1"
    if [[ "$1" == fleet ]]; then
      echo "::error::key_escrow: the fleet identity is retired, not rotated (faas_compute_fleet_key_retired, ADR 20261022111812)" >&2
      exit 2
    fi
    recipient=$(remote "$rotate_script" "$remote_dir" "$1" "$(gregalectl_domain "$1")" "${KEY_ESCROW_GREGALECTL:-/usr/local/bin/gregalectl}")
    if ! [[ "$recipient" =~ ^age1[0-9a-z]+$ ]]; then
      echo "::error::key_escrow: rotate returned no recipient" >&2
      exit 1
    fi
    echo "$1 rotated; set SEAL_DOMAIN_$(printf '%s' "$1" | tr '[:lower:]' '[:upper:]')_AGE_RECIPIENT to:"
    echo "$recipient"
    ;;
  retire)
    [[ $# -eq 1 ]] || usage
    check_name "$1"
    echo "$1: $(remote "$retire_script" "$remote_dir" "$1")"
    ;;
  *)
    usage
    ;;
esac
