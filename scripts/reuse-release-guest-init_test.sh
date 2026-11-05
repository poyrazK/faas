#!/usr/bin/env bash
# Tests for reuse-release-guest-init.sh (ADR-20261105150000).
#
# The release keeps the previous signed release's guest-init while the new one
# runs the same code, so the runtime bases keep their bytes and ADR-510 keeps
# restoring snapshots. This file runs the script in a throwaway repository with
# stub gh and cosign commands and ELF fixtures built in memory: no network, no
# Go build. The cases that matter are the two verdicts (reuse on the same code,
# ship the new binary on different code) and that a prior release that fails
# verification is fatal rather than quietly skipped.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gregale-guest-init-reuse-test.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

for tool in jq python3 git tar sha256sum; do
	command -v "$tool" >/dev/null 2>&1 || {
		printf 'skip: reuse-release-guest-init (%s not available)\n' "$tool"
		exit 0
	}
done

# A repository with the script, two commits and a tag on the first: the
# release being built is the second, the prior signed release the first.
SANDBOX="$TMP_DIR/repo"
mkdir -p "$SANDBOX/scripts/ci"
cp "$REPO_ROOT/scripts/reuse-release-guest-init.sh" "$SANDBOX/scripts/"
cp "$REPO_ROOT/scripts/ci/guest_init_reuse.py" "$REPO_ROOT/scripts/ci/binary_code.py" "$SANDBOX/scripts/ci/"
git_in() { git -C "$SANDBOX" -c user.name=test -c user.email=test@example.com "$@"; }
git_in init -q
git_in add -A
git_in commit -q -m prior
git_in tag v1.0.0
git_in commit -q --allow-empty -m head
HEAD_SHA="$(git_in rev-parse HEAD)"
PRIOR_SHA="$(git_in rev-parse v1.0.0)"

# Stubs: gh lists FAKE_ASSETS for any tag and "downloads" the fixture release;
# cosign exits FAKE_COSIGN_STATUS.
STUBS="$TMP_DIR/stubs"
mkdir -p "$STUBS"
cat >"$STUBS/gh" <<'EOF'
#!/usr/bin/env bash
case "$1 $2" in
"release view") printf '%b\n' "${FAKE_ASSETS-release.tar.gz\nrelease.cosign.bundle}" ;;
"release download")
	dir=""
	while [[ $# -gt 0 ]]; do
		[[ "$1" != --dir ]] || dir=$2
		shift
	done
	cp "$FIXTURE/release.tar.gz" "$FIXTURE/release.cosign.bundle" "$dir/"
	;;
*) echo "stub gh: unexpected $*" >&2; exit 2 ;;
esac
EOF
cat >"$STUBS/cosign" <<'EOF'
#!/usr/bin/env bash
exit "${FAKE_COSIGN_STATUS:-0}"
EOF
chmod +x "$STUBS/gh" "$STUBS/cosign"

# make_init FILE BUILD_ID TEXT_BYTE: a guest-init-shaped ELF. Two builds of the
# same code differ in the Go build ID note and the line table; TEXT_BYTE is the
# code.
make_init() {
	REPO_ROOT="$REPO_ROOT" python3 -I - "$1" "$2" "$3" <<'EOF'
import importlib.util, os, sys
path, build_id, text_byte = sys.argv[1], sys.argv[2].encode(), int(sys.argv[3], 16)
spec = importlib.util.spec_from_file_location("t", os.path.join(os.environ["REPO_ROOT"], "scripts/ci/guest_init_reuse_test.py"))
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
lines = b"L" * 32 if build_id == b"prior" else b"X" * 40
open(path, "wb").write(mod.guest_init(text=bytes([text_byte]) * 16, build_id=build_id, lines=lines))
EOF
}

# make_release GIT_SHA INIT_FILE [DIGEST]: the previous release's tarball and
# bundle in $FIXTURE. DIGEST overrides the init digest the manifest records;
# "none" leaves it out.
make_release() {
	local sha=$1 init=$2 digest=${3:-}
	[[ -n "$digest" ]] || digest="sha256:$(sha256sum "$init" | awk '{print $1}')"
	FIXTURE="$FIXTURE" python3 -I - "$sha" "$init" "$digest" <<'EOF'
import json, os, sys, tarfile
sha, init, digest = sys.argv[1:4]
fixture = os.environ["FIXTURE"]
manifest = {"git_sha": sha, "tool_hashes": {} if digest == "none" else {"init": digest}}
with open(os.path.join(fixture, "release-manifest.json"), "w") as fh:
    json.dump(manifest, fh)
with tarfile.open(os.path.join(fixture, "release.tar.gz"), "w:gz") as tar:
    tar.add(os.path.join(fixture, "release-manifest.json"), arcname="release-manifest.json")
    tar.add(init, arcname="init")
open(os.path.join(fixture, "release.cosign.bundle"), "w").write("{}")
EOF
}

# run_case NAME WANT_STATUS: run the script on a copy of the head build in
# $HEAD_INIT and compare its status. The copy is left in $WORK_INIT.
run_case() {
	local name=$1 want=$2 status=0
	cp "$HEAD_INIT" "$WORK_INIT"
	(
		cd "$SANDBOX"
		PATH="$STUBS:$PATH" FIXTURE="$FIXTURE" GITHUB_REPOSITORY=example/repo GITHUB_REF_NAME=v1.0.1 \
			bash scripts/reuse-release-guest-init.sh --init "$WORK_INIT" --git-sha "$HEAD_SHA" >"$TMP_DIR/out.txt" 2>&1
	) || status=$?
	[[ "$status" == "$want" ]] || {
		cat "$TMP_DIR/out.txt" >&2
		fail "$name: exit status $status, want $want"
	}
}

FIXTURE="$TMP_DIR/fixture"
mkdir -p "$FIXTURE"
PRIOR_INIT="$TMP_DIR/init-prior"
HEAD_INIT="$TMP_DIR/init-head"
WORK_INIT="$TMP_DIR/init-work"
make_init "$PRIOR_INIT" prior 90

# --- The same code, built again: only the build ID and line table differ.
make_init "$HEAD_INIT" head 90
cmp -s "$PRIOR_INIT" "$HEAD_INIT" && fail "fixture: the two builds should differ in bytes"
make_release "$PRIOR_SHA" "$PRIOR_INIT"
run_case "same code is reused" 0
cmp -s "$PRIOR_INIT" "$WORK_INIT" || fail "same code: the head build was not replaced by the prior release's bytes"
grep -q "reused guest-init from v1.0.0" "$TMP_DIR/out.txt" || fail "same code: no reuse message"
[[ -x "$WORK_INIT" ]] || fail "same code: the reused guest-init is not executable"

# --- Reusing again changes nothing: the prior bytes compare as the same.
cp "$PRIOR_INIT" "$HEAD_INIT"
run_case "identical bytes are reused" 0
cmp -s "$PRIOR_INIT" "$WORK_INIT" || fail "identical bytes: init changed"

# --- Different code ships the new binary.
make_init "$HEAD_INIT" head cc
run_case "different code ships the new binary" 3
cmp -s "$HEAD_INIT" "$WORK_INIT" || fail "different code: the head build was replaced"
grep -q "shipping the new binary" "$TMP_DIR/out.txt" || fail "different code: no explanation"

make_init "$HEAD_INIT" head 90

# --- No prior signed release (the tag has no release assets).
(
	export FAKE_ASSETS=""
	run_case "no prior signed release" 3
)
cmp -s "$HEAD_INIT" "$WORK_INIT" || fail "no prior release: the head build was replaced"

# --- A prior release whose manifest does not record guest-init (from before the
# manifest hashed it): nothing to verify the bytes against.
make_release "$PRIOR_SHA" "$PRIOR_INIT" none
run_case "a manifest without a guest-init digest" 3
cmp -s "$HEAD_INIT" "$WORK_INIT" || fail "no digest: the head build was replaced"

# --- Integrity failures are fatal, never a quiet fallback.
make_release "$PRIOR_SHA" "$PRIOR_INIT" "sha256:$(printf '0%.0s' {1..64})"
run_case "a guest-init that does not match the signed digest" 1
make_release "$PRIOR_SHA" "$PRIOR_INIT" "sha256:short"
run_case "an invalid digest in the manifest" 1
make_release "$HEAD_SHA" "$PRIOR_INIT"
run_case "a manifest for another commit" 1
make_release "$PRIOR_SHA" "$PRIOR_INIT"
(
	export FAKE_COSIGN_STATUS=1
	run_case "a bundle that does not verify" 1
)
printf '\x00not an elf' >"$FIXTURE/bad-init"
make_release "$PRIOR_SHA" "$FIXTURE/bad-init"
run_case "a prior guest-init that is not an ELF binary" 1
cmp -s "$HEAD_INIT" "$WORK_INIT" || fail "integrity failures: the head build was replaced"

# --- Usage errors.
status=0
(cd "$SANDBOX" && GITHUB_REPOSITORY=x bash scripts/reuse-release-guest-init.sh --init "$HEAD_INIT" --git-sha nope) >/dev/null 2>&1 || status=$?
[[ "$status" == 2 ]] || fail "a bad --git-sha should be a usage error (2), got $status"

printf 'ok: reuse-release-guest-init\n'
