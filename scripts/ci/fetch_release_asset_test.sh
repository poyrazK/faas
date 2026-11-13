#!/usr/bin/env bash
# Hermetic tests for fetch-release-asset.sh and its callers (install-crane.sh,
# install-grype.sh): curl and gh are stubbed, so nothing is downloaded. The
# stubs write bytes that do not match the pin, which also proves a fallback
# download cannot install anything but the pinned asset.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
installer="$repo_root/scripts/ci/install-crane.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT
mkdir -p "$test_root/stub"
fail() { echo "FAIL: $*" >&2; exit 1; }

# curl stub: CURL_MODE=fail exits 22 like a 504; otherwise writes junk to -o.
cat > "$test_root/stub/curl" <<'EOF'
#!/usr/bin/env bash
printf 'curl %s\n' "$*" >> "$STUB_LOG"
[[ "${CURL_MODE:-}" == fail ]] && exit 22
out=""
while [[ $# -gt 0 ]]; do [[ "$1" == -o ]] && { out="$2"; shift; }; shift; done
printf 'not-crane' > "$out"
EOF
# gh stub: records its arguments and writes junk into --dir.
cat > "$test_root/stub/gh" <<'EOF'
#!/usr/bin/env bash
printf 'gh %s\n' "$*" >> "$STUB_LOG"
dir=""; pattern=""
while [[ $# -gt 0 ]]; do
  case "$1" in --dir) dir="$2"; shift ;; --pattern) pattern="$2"; shift ;; esac
  shift
done
printf 'not-crane-either' > "$dir/$pattern"
EOF
chmod +x "$test_root/stub/curl" "$test_root/stub/gh"
export PATH="$test_root/stub:$PATH" STUB_LOG="$test_root/stub.log"

# 1. The release download fails: the API fallback is used, and its bytes are
#    still checked against the pin (the stub's junk is rejected).
: > "$STUB_LOG"
if CURL_MODE=fail bash "$installer" "$test_root/dest1" 2>"$test_root/err1"; then
  fail "a fallback download that does not match the pin was installed"
fi
grep -q '^gh release download v0.21.1 --repo google/go-containerregistry --pattern go-containerregistry_Linux_x86_64.tar.gz --dir ' "$STUB_LOG" \
  || fail "the failed release download did not fall back to gh release download: $(cat "$STUB_LOG")"
grep -q 'release download of google/go-containerregistry v0.21.1 go-containerregistry_Linux_x86_64.tar.gz failed; fetching the same asset through the GitHub API' "$test_root/err1" || fail "the fallback is not logged: $(cat "$test_root/err1")"
grep -q 'FAILED' "$test_root/err1" || fail "the checksum mismatch is not reported: $(cat "$test_root/err1")"
[[ ! -e "$test_root/dest1/crane" ]] || fail "crane was installed from unverified bytes"

# 2. The release download succeeds: no fallback, and the pin still applies.
: > "$STUB_LOG"
if bash "$installer" "$test_root/dest2" 2>"$test_root/err2"; then
  fail "a release download that does not match the pin was installed"
fi
grep -q '^gh ' "$STUB_LOG" && fail "gh ran although the release download succeeded"
grep -q -- '--proto =https' "$STUB_LOG" || fail "curl must refuse non-HTTPS redirects"
[[ ! -e "$test_root/dest2/crane" ]] || fail "crane was installed from unverified bytes"

# 3. A matching asset is kept at OUT; the fetcher works for any repo and tag.
printf 'pinned-bytes' > "$test_root/pinned"
pin="$(sha256sum "$test_root/pinned" | cut -d' ' -f1)"
cat > "$test_root/stub/curl" <<'EOF2'
#!/usr/bin/env bash
printf 'curl %s\n' "$*" >> "$STUB_LOG"
out=""
while [[ $# -gt 0 ]]; do [[ "$1" == -o ]] && { out="$2"; shift; }; shift; done
printf 'pinned-bytes' > "$out"
EOF2
: > "$STUB_LOG"
bash "$repo_root/scripts/ci/fetch-release-asset.sh" sigstore/cosign v2.4.1 cosign-linux-amd64 "$pin" "$test_root/out/cosign" \
  || fail "a matching asset was rejected"
[[ "$(cat "$test_root/out/cosign")" == pinned-bytes ]] || fail "the verified asset was not written to OUT"
grep -q 'https://github.com/sigstore/cosign/releases/download/v2.4.1/cosign-linux-amd64' "$STUB_LOG" || fail "wrong download URL: $(cat "$STUB_LOG")"

# 4. Grype installs through the fetcher too (pin enforced).
: > "$STUB_LOG"
if GRYPE_VERSION=0.116.0 GRYPE_SHA256=0000000000000000000000000000000000000000000000000000000000000000 RUNNER_TEMP="$test_root/rt" \
  bash "$repo_root/scripts/ci/install-grype.sh" "$test_root/grype" 2>"$test_root/err4"; then
  fail "install-grype.sh installed an asset that does not match its pin"
fi
grep -q 'grype_0.116.0_linux_amd64.tar.gz checksum FAILED' "$test_root/err4" || fail "install-grype.sh does not use the fetcher: $(cat "$test_root/err4")"

# 5. No release-path workflow or installer downloads a GitHub release asset
#    directly: every one goes through fetch-release-asset.sh.
for file in .github/workflows/release.yml .github/workflows/images.yml scripts/ci/install-grype.sh scripts/ci/install-crane.sh; do
  if grep -n 'https://github.com/[^ ]*/releases/download' "$repo_root/$file" | grep -v '^[0-9]*: *#'; then
    fail "$file downloads a GitHub release asset directly; use scripts/ci/fetch-release-asset.sh"
  fi
done

echo "fetch_release_asset_test: ok"
