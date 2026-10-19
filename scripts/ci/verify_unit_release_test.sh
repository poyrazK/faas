#!/usr/bin/env bash
# Hermetic tests for verify_unit_release.sh: cosign is stubbed, so the
# signature is taken as valid and the tests cover everything after it.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
verifier="$repo_root/scripts/ci/verify_unit_release.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT
mkdir -p "$test_root/stub"
cat > "$test_root/stub/cosign" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" > "$COSIGN_LOG"
EOF
chmod +x "$test_root/stub/cosign"
export PATH="$test_root/stub:$PATH" COSIGN_LOG="$test_root/cosign.log"
fail() { echo "FAIL: $*" >&2; exit 1; }
commit=1111111111111111111111111111111111111111

# artifact NAME [mutation]: build a release dir the way release-units.yml does.
artifact() {
  local dir="$test_root/$1" src="$test_root/$1-src"
  mkdir -p "$dir" "$src/bin" "$src/tools"
  printf 'apid-binary' > "$src/bin/apid"
  printf 'deployctl-binary' > "$src/tools/deployctl"
  local digest tool
  digest="$(sha256sum "$src/bin/apid" | awk '{print $1}')"
  tool="$(sha256sum "$src/tools/deployctl" | awk '{print $1}')"
  jq -n --arg sha "$digest" --arg tool "$tool" --arg commit "$commit" \
    '{format_version: 2, kind: "gregale-unit-release", target: "linux/amd64", git_sha: $commit,
      units: {apid: {lane: "control-plane", sha256: $sha}}, tools: {deployctl: {sha256: $tool}}}' > "$src/units-manifest.json"
  case "${2:-}" in
    extra-binary) printf 'x' > "$src/bin/meterd" ;;
    extra-tool) printf 'x' > "$src/tools/migrate" ;;
    bad-tool) printf 'tampered' > "$src/tools/deployctl" ;;
    no-deployctl) jq 'del(.tools.deployctl)' "$src/units-manifest.json" > "$src/m" && mv "$src/m" "$src/units-manifest.json" && rm "$src/tools/deployctl" ;;
    bad-digest) printf 'tampered' > "$src/bin/apid" ;;
    old-format) jq '.format_version = 1' "$src/units-manifest.json" > "$src/m" && mv "$src/m" "$src/units-manifest.json" ;;
    data|data-ineligible|data-no-migrate)
      printf 'migrate-binary' > "$src/tools/migrate"
      jq --arg m "$(sha256sum "$src/tools/migrate" | awk '{print $1}')" --arg kind "$2" '
        .tools.migrate = {sha256: $m}
        | .data = {eligible: true, version: "data-1111111111111111111111111111111111111111",
                   migrations: [{path: "migrations/20261018000000001_a.sql", eligible: ($kind != "data-ineligible")}]}
        | if $kind == "data-no-migrate" then del(.tools.migrate) else . end' \
        "$src/units-manifest.json" > "$src/m" && mv "$src/m" "$src/units-manifest.json"
      [[ "$2" != data-no-migrate ]] || rm "$src/tools/migrate" ;;
    host|host-no-gregalectl|host-no-roles)
      printf 'gregalectl-binary' > "$src/tools/gregalectl"
      jq --arg g "$(sha256sum "$src/tools/gregalectl" | awk '{print $1}')" --arg kind "$2" '
        .tools.gregalectl = {sha256: $g}
        | .host_config = {group: "control_plane", roles: (if $kind == "host-no-roles" then [] else ["postgres_capacity"] end),
                          all: false, commit: "1111111111111111111111111111111111111111",
                          version: "host-1111111111111111111111111111111111111111"}
        | if $kind == "host-no-gregalectl" then del(.tools.gregalectl) else . end' \
        "$src/units-manifest.json" > "$src/m" && mv "$src/m" "$src/units-manifest.json"
      [[ "$2" != host-no-gregalectl ]] || rm "$src/tools/gregalectl" ;;
  esac
  tar -czf "$dir/units.tar.gz" -C "$src" bin tools units-manifest.json
  cp "$src/units-manifest.json" "$dir/units-manifest.json"
  if [[ "${2:-}" == other-manifest ]]; then
    jq '.git_sha = "2222222222222222222222222222222222222222"' "$src/units-manifest.json" > "$dir/units-manifest.json"
  fi
  printf 'bundle' > "$dir/units.cosign.bundle"
  (cd "$dir" && sha256sum units.tar.gz units.cosign.bundle units-manifest.json > SHA256SUMS)
  printf '%s\n' "$dir"
}

dir="$(artifact good)"
[[ "$(bash "$verifier" "$dir" 2>/dev/null)" == "$commit" ]] || fail "a valid release did not verify"
grep -Fq -- "--certificate-identity https://github.com/poyrazK/faas/.github/workflows/release-units.yml@refs/heads/main" \
  "$COSIGN_LOG" || fail "cosign was not asked for the release-units.yml identity on main"
[[ -s "$dir/unpacked/bin/apid" && -s "$dir/unpacked/tools/deployctl" ]] || fail "release not unpacked"

dir="$(artifact data data)"
[[ "$(bash "$verifier" "$dir" 2>/dev/null)" == "$commit" ]] || fail "a release with a lane-eligible data step did not verify"

dir="$(artifact host host)"
[[ "$(bash "$verifier" "$dir" 2>/dev/null)" == "$commit" ]] || fail "a release with a host-config step did not verify"
[[ -s "$dir/unpacked/tools/gregalectl" ]] || fail "the host-config step's gregalectl was not unpacked"

for mutation in extra-binary extra-tool bad-digest bad-tool no-deployctl old-format other-manifest data-ineligible data-no-migrate \
  host-no-gregalectl host-no-roles; do
  dir="$(artifact "$mutation" "$mutation")"
  if bash "$verifier" "$dir" >/dev/null 2>&1; then fail "verified a release with $mutation"; fi
done

dir="$(artifact tampered-sums)"
printf 'changed' >> "$dir/units.tar.gz"
if bash "$verifier" "$dir" >/dev/null 2>&1; then fail "verified a tarball that does not match SHA256SUMS"; fi

echo "verify_unit_release tests passed"
