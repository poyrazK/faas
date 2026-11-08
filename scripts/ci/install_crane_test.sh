#!/usr/bin/env bash
# Hermetic tests for install-crane.sh: curl and gh are stubbed, so nothing is
# downloaded. The stubs write bytes that do not match the pin, which also
# proves a fallback download cannot install anything but the pinned crane.
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
grep -q 'fetching the same asset through the GitHub API' "$test_root/err1" || fail "the fallback is not logged"
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

# 3. Every crane download in the release path goes through the installer.
for workflow in release.yml images.yml; do
  if grep -q 'go-containerregistry/releases/download' "$repo_root/.github/workflows/$workflow"; then
    fail "$workflow downloads crane directly; use scripts/ci/install-crane.sh"
  fi
done

echo "install_crane_test: ok"
