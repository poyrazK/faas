#!/usr/bin/env bash
# Hermetic tests for key_escrow.sh and seal_domain_vars.sh's escrow key source
# (ADR-20261110210000 §2). A fake ssh runs the remote command locally.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
escrow="$repo_root/scripts/ci/key_escrow.sh"
vars_script="$repo_root/scripts/ci/seal_domain_vars.sh"
work="$(mktemp -d)"
trap 'chmod -R u+w "$work" 2>/dev/null || true; rm -rf "$work"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

key() { printf 'AGE-SECRET-KEY-1%s' "$(printf '%s' "$1" | tr '[:lower:]' '[:upper:]')"; }
pub() { printf 'age1%s' "$1"; }

# Fake ssh: drop options, check the target, run the command here. It records
# every command line so the test can prove no identity is passed as an arg.
cat > "$work/ssh" <<'SSH'
#!/usr/bin/env bash
set -euo pipefail
while [[ $# -gt 0 ]]; do
  case "$1" in
    -i | -o) shift 2 ;;
    *) break ;;
  esac
done
[[ "$1" == root@cp.test ]] || { echo "fake ssh: unexpected target $1" >&2; exit 255; }
shift
printf '%s\n' "$*" >> "$FAKE_SSH_LOG"
eval "$*"
SSH
chmod 0755 "$work/ssh"
printf 'k\n' > "$work/ssh-key"
printf 'cp.test ssh-ed25519 AAAA\n' > "$work/known"
dir="$work/escrow"
base=(env -i PATH="$PATH" KEY_ESCROW_SSH="$work/ssh" KEY_ESCROW_HOST=cp.test
  KEY_ESCROW_SSH_KEY="$work/ssh-key" KEY_ESCROW_KNOWN_HOSTS="$work/known"
  KEY_ESCROW_DIR="$dir" FAKE_SSH_LOG="$work/ssh.log")

# put escrows each set secret, skips unset ones.
out=$("${base[@]}" FLEET_AGE_KEY="$(key fleet)" SEAL_DOMAIN_RUNTIME_AGE_KEY="$(key runtime)" \
  SEAL_DOMAIN_EDGE_TLS_AGE_KEY="$(key edgetls)" \
  bash "$escrow" put fleet runtime edge_tls build) || fail "put failed: $out"
grep -qx 'fleet: escrowed' <<<"$out" || fail "fleet not escrowed: $out"
grep -qx 'runtime: escrowed' <<<"$out" || fail "runtime not escrowed: $out"
grep -qx 'edge_tls: escrowed' <<<"$out" || fail "edge_tls not escrowed: $out"
grep -q '^build: skipped' <<<"$out" || fail "unset build was not skipped: $out"
[[ "$(<"$dir/runtime.age")" == "$(key runtime)" ]] || fail "runtime escrow content differs"
[[ "$(stat -c %a "$dir/runtime.age" 2>/dev/null || stat -f %Lp "$dir/runtime.age")" == 400 ]] || fail "escrowed file is not 0400"
[[ "$(stat -c %a "$dir" 2>/dev/null || stat -f %Lp "$dir")" == 700 ]] || fail "escrow dir is not 0700"
for value in "$(key fleet)" "$(key runtime)" "$(key edgetls)"; do
  ! grep -qF "$value" "$work/ssh.log" || fail "an identity appeared on an ssh command line"
done

# put again with the same value is a no-op; a different value is refused.
out=$("${base[@]}" SEAL_DOMAIN_RUNTIME_AGE_KEY="$(key runtime)" bash "$escrow" put runtime) || fail "idempotent put failed"
grep -qx 'runtime: unchanged' <<<"$out" || fail "same value was not unchanged: $out"
if "${base[@]}" SEAL_DOMAIN_RUNTIME_AGE_KEY="$(key other)" bash "$escrow" put runtime >/dev/null 2>&1; then
  fail "put replaced an escrowed identity with a different one"
fi
[[ "$(<"$dir/runtime.age")" == "$(key runtime)" ]] || fail "refused put still changed the escrow"
if "${base[@]}" SEAL_DOMAIN_BUILD_AGE_KEY="not-a-key" bash "$escrow" put build >/dev/null 2>&1; then
  fail "put accepted a value that is not an age identity"
fi
if "${base[@]}" bash "$escrow" put 'runtime;rm' >/dev/null 2>&1; then
  fail "put accepted a name outside the allowlist"
fi

# status names presence only.
out=$("${base[@]}" bash "$escrow" status runtime build)
[[ "$out" == $'runtime present\nbuild missing' ]] || fail "status: $out"

# fetch copies exactly the asked identities, 0400; a missing one fails.
"${base[@]}" bash "$escrow" fetch "$work/fetched" runtime fleet || fail "fetch failed"
[[ "$(<"$work/fetched/runtime.age")" == "$(key runtime)" ]] || fail "fetched runtime differs"
[[ -s "$work/fetched/fleet.age" && ! -e "$work/fetched/edge_tls.age" ]] || fail "fetch copied the wrong set"
if "${base[@]}" bash "$escrow" fetch "$work/fetched2" runtime build >/dev/null 2>&1; then
  fail "fetch of a missing identity succeeded"
fi
# Without SSH settings nothing runs.
if env -i PATH="$PATH" bash "$escrow" status runtime >/dev/null 2>&1; then
  fail "status ran without KEY_ESCROW_HOST"
fi

# seal_domain_vars.sh, key source escrow.
recipients=(SEAL_DOMAIN_RUNTIME_AGE_RECIPIENT="$(pub runtime)" SEAL_DOMAIN_EDGE_TLS_AGE_RECIPIENT="$(pub edgetls)"
  SEAL_DOMAIN_OUTBOUND_AGE_RECIPIENT="$(pub outbound)")
# --identities lists what the role's hosts need, from the recipients alone.
got=$(env -i PATH="$PATH" SEAL_DOMAIN_KEY_SOURCE=escrow "${recipients[@]}" bash "$vars_script" --identities compute)
[[ "$got" == $'runtime\nedge_tls' ]] || fail "compute identities: $got"
got=$(env -i PATH="$PATH" SEAL_DOMAIN_KEY_SOURCE=escrow "${recipients[@]}" bash "$vars_script" --identities control-plane)
[[ "$got" == outbound ]] || fail "control-plane identities: $got"

# Compute staging reads the fetched files and needs no private secret.
env -i PATH="$PATH" SEAL_DOMAIN_KEY_SOURCE=escrow SEAL_DOMAIN_ESCROW_DIR="$work/fetched" \
  SEAL_DOMAIN_RUNTIME_AGE_RECIPIENT="$(pub runtime)" \
  bash "$vars_script" compute "$work/c/out" "$work/c/vars.yml" || fail "escrow compute staging failed"
[[ "$(<"$work/c/out/runtime.age")" == "$(key runtime)" ]] || fail "staged runtime identity differs from the escrow"
grep -qxF "faas_seal_domain_runtime_identity_src: \"$work/c/out/runtime.age\"" "$work/c/vars.yml" || fail "runtime src missing"
grep -q 'from the control-plane key escrow' "$work/c/vars.yml" || fail "vars file does not record the escrow source"

# A domain with a recipient but no escrowed identity fails closed.
if env -i PATH="$PATH" SEAL_DOMAIN_KEY_SOURCE=escrow SEAL_DOMAIN_ESCROW_DIR="$work/fetched" \
  SEAL_DOMAIN_BUILD_AGE_RECIPIENT="$(pub build)" \
  bash "$vars_script" compute "$work/d/out" "$work/d/vars.yml" >/dev/null 2>&1; then
  fail "staging succeeded without the escrowed build identity"
fi
# Any private identity still held as a secret refuses the escrow mode.
for leaked in SEAL_DOMAIN_RUNTIME_AGE_KEY="$(key runtime)" FLEET_AGE_KEY="$(key fleet)"; do
  if env -i PATH="$PATH" SEAL_DOMAIN_KEY_SOURCE=escrow SEAL_DOMAIN_ESCROW_DIR="$work/fetched" \
    SEAL_DOMAIN_RUNTIME_AGE_RECIPIENT="$(pub runtime)" "$leaked" \
    bash "$vars_script" compute "$work/e/out" "$work/e/vars.yml" >/dev/null 2>&1; then
    fail "escrow mode ran with ${leaked%%=*} still set"
  fi
done
# Escrow mode without a fetched directory fails; an unknown source fails.
if env -i PATH="$PATH" SEAL_DOMAIN_KEY_SOURCE=escrow SEAL_DOMAIN_RUNTIME_AGE_RECIPIENT="$(pub runtime)" \
  bash "$vars_script" compute "$work/f/out" "$work/f/vars.yml" >/dev/null 2>&1; then
  fail "escrow mode ran without SEAL_DOMAIN_ESCROW_DIR"
fi
if env -i PATH="$PATH" SEAL_DOMAIN_KEY_SOURCE=vault bash "$vars_script" compute "$work/g/out" "$work/g/vars.yml" >/dev/null 2>&1; then
  fail "an unknown key source was accepted"
fi

# rotate: the new identity is generated on the "control plane" by gregalectl,
# the old one is kept as .previous, and only the recipient is printed.
cat > "$work/gregalectl" <<'G'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1 $2" == "seal-domain init" ]] || exit 9
domain="" dir=""
shift 2
while [[ $# -gt 0 ]]; do
  case "$1" in --domain) domain=$2; shift 2 ;; --dir) dir=$2; shift 2 ;; *) exit 9 ;; esac
done
mkdir -p "$dir"
printf 'AGE-SECRET-KEY-1ROTATED%s' "$(printf '%s' "$domain" | tr -d - | tr '[:lower:]' '[:upper:]')" > "$dir/$domain.age"
printf 'age1rotated%s' "$(printf '%s' "$domain" | tr -d -)" > "$dir/$domain.age.pub"
G
chmod 0755 "$work/gregalectl"
rot=("${base[@]}" KEY_ESCROW_GREGALECTL="$work/gregalectl")
out=$("${rot[@]}" bash "$escrow" rotate edge_tls) || fail "rotate failed: $out"
[[ "$(tail -n1 <<<"$out")" == age1rotatededgetls ]] || fail "rotate did not print the new recipient: $out"
! grep -q 'AGE-SECRET-KEY' <<<"$out" || fail "rotate printed an identity"
[[ "$(<"$dir/edge_tls.age")" == AGE-SECRET-KEY-1ROTATEDEDGETLS ]] || fail "rotate did not install the new identity"
[[ "$(<"$dir/edge_tls.age.previous")" == "$(key edgetls)" ]] || fail "rotate did not keep the old identity as previous"
if "${rot[@]}" bash "$escrow" rotate edge_tls >/dev/null 2>&1; then
  fail "a second rotate ran while the previous identity was still kept"
fi
if "${rot[@]}" bash "$escrow" rotate fleet >/dev/null 2>&1; then
  fail "rotate accepted the fleet identity"
fi
if "${rot[@]}" bash "$escrow" rotate build >/dev/null 2>&1; then
  fail "rotate ran without a current identity"
fi
out=$("${base[@]}" bash "$escrow" status edge_tls)
[[ "$out" == "edge_tls present (previous kept: rotation in progress)" ]] || fail "status during rotation: $out"

# fetch carries the previous identity, and staging hands it to the role.
"${base[@]}" bash "$escrow" fetch "$work/fetched3" edge_tls || fail "fetch during rotation failed"
[[ "$(<"$work/fetched3/edge_tls.age.previous")" == "$(key edgetls)" ]] || fail "fetch dropped the previous identity"
env -i PATH="$PATH" SEAL_DOMAIN_KEY_SOURCE=escrow SEAL_DOMAIN_ESCROW_DIR="$work/fetched3" \
  SEAL_DOMAIN_EDGE_TLS_AGE_RECIPIENT=age1rotatededgetls \
  bash "$vars_script" compute "$work/r/out" "$work/r/vars.yml" || fail "staging during rotation failed"
grep -qxF "faas_seal_domain_edge_tls_identity_src: \"$work/r/out/edge_tls.age\"" "$work/r/vars.yml" || fail "rotated identity src missing"
grep -qxF "faas_seal_domain_edge_tls_previous_identity_src: \"$work/r/out/edge_tls.age.previous\"" "$work/r/vars.yml" || fail "previous identity src missing"
[[ "$(<"$work/r/out/edge_tls.age.previous")" == "$(key edgetls)" ]] || fail "staged previous identity differs"

# retire drops the previous identity; a second retire is a no-op.
out=$("${base[@]}" bash "$escrow" retire edge_tls)
[[ "$out" == "edge_tls: retired" && ! -e "$dir/edge_tls.age.previous" ]] || fail "retire: $out"
out=$("${base[@]}" bash "$escrow" retire edge_tls)
[[ "$out" == "edge_tls: no previous edge_tls identity is escrowed" ]] || fail "second retire: $out"

echo "key_escrow: ok"
