#!/usr/bin/env bash
# Tests for reuse-release-guest-init.sh (ADR-20261105150000).
#
# The release keeps the previous signed release's guest-init while the new one
# runs the same code, so the runtime bases keep their bytes and ADR-510 keeps
# restoring snapshots. This file runs the script in a throwaway repository with
# stub gh and cosign commands and ELF fixtures built in memory: no network, no
# Go build. The cases that matter are the two verdicts (reuse on the same code,
# ship the new binary on different code) and that a prior release that fails
# verification is fatal rather than quietly skipped. With GUEST_INIT_HOLD_HOURS
# set (the release train), code changed only through pkg/api is held unless
# the held code is too old or a commit asks to ship; a guest change ships.
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
GIT_COMMITTER_DATE="$(($(date +%s) - 10 * 86400)) +0000" git_in commit -q -m ancient
ANCIENT_SHA="$(git_in rev-parse HEAD)"
git_in commit -q --allow-empty -m prior
git_in tag v1.0.0
git_in commit -q --allow-empty -m head
HEAD_SHA="$(git_in rev-parse HEAD)"
PRIOR_SHA="$(git_in rev-parse v1.0.0)"
# Three more releases built on head: one changes guest-init itself, one changes
# pkg/api and asks to ship in a commit trailer, one only changes pkg/api.
mkdir -p "$SANDBOX/guest/init"
echo package main >"$SANDBOX/guest/init/secrets_linux.go"
echo package main >"$SANDBOX/guest/init/secrets_linux_test.go"
git_in add -A
git_in commit -q -m "guest-init: secret fix"
GUEST_SHA="$(git_in rev-parse HEAD)"
git_in checkout -q "$HEAD_SHA"
mkdir -p "$SANDBOX/pkg/api"
echo package api >"$SANDBOX/pkg/api/limits.go"
git_in add -A
git_in commit -q -m "api: a limit the guest reads" -m "Guest-Init-Ship: now"
TRAILER_SHA="$(git_in rev-parse HEAD)"
git_in checkout -q "$HEAD_SHA"
mkdir -p "$SANDBOX/pkg/api"
echo package api >"$SANDBOX/pkg/api/limits.go"
git_in add -A
git_in commit -q -m "api: a new plan limit"
ORDINARY_SHA="$(git_in rev-parse HEAD)"

# Stubs: gh lists FAKE_ASSETS for any tag and "downloads" the fixture release;
# cosign exits FAKE_COSIGN_STATUS.
STUBS="$TMP_DIR/stubs"
mkdir -p "$STUBS"
cat >"$STUBS/gh" <<'EOF'
#!/usr/bin/env bash
case "$1 $2" in
"release view") printf '%b\n' "${FAKE_ASSETS-release.tar.gz\nrelease.cosign.bundle}" ;;
"release download")
	dir="" provenance=""
	while [[ $# -gt 0 ]]; do
		[[ "$1" != --dir ]] || dir=$2
		[[ "$1 ${2:-}" != "--pattern guest-init.json" ]] || provenance=1
		shift
	done
	if [[ -n "$provenance" ]]; then
		[[ -f "$FIXTURE/guest-init.json" ]] || exit 1
		cp "$FIXTURE/guest-init.json" "$dir/"
	else
		cp "$FIXTURE/release.tar.gz" "$FIXTURE/release.cosign.bundle" "$dir/"
	fi
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
# $HEAD_INIT and compare its status. The copy is left in $WORK_INIT and the
# provenance it wrote in $PROVENANCE. RUN_SHA picks the release commit.
run_case() {
	local name=$1 want=$2 status=0
	cp "$HEAD_INIT" "$WORK_INIT"
	rm -f "$PROVENANCE"
	(
		cd "$SANDBOX"
		PATH="$STUBS:$PATH" FIXTURE="$FIXTURE" GITHUB_REPOSITORY=example/repo GITHUB_REF_NAME=v1.0.1 \
			bash scripts/reuse-release-guest-init.sh --init "$WORK_INIT" --git-sha "${RUN_SHA:-$HEAD_SHA}" \
			--provenance-out "$PROVENANCE" >"$TMP_DIR/out.txt" 2>&1
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
PROVENANCE="$TMP_DIR/provenance.json"

# want_source NAME SHA: the provenance names SHA as the shipped code's source
# and the digest of the bytes in $WORK_INIT.
want_source() {
	local got digest
	got=$(jq -r .source_sha "$PROVENANCE") || fail "$1: no provenance written"
	[[ "$got" == "$2" ]] || fail "$1: provenance source $got, want $2"
	digest="sha256:$(sha256sum "$WORK_INIT" | awk '{print $1}')"
	[[ "$(jq -r .digest "$PROVENANCE")" == "$digest" ]] || fail "$1: provenance digest is not the shipped bytes'"
}
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
want_source "different code" "$HEAD_SHA"

# --- The release train: changed code is held behind the prior bytes.
export GUEST_INIT_HOLD_HOURS=72
export GUEST_INIT_CLOSURE=$'guest/init\npkg/api'
RUN_SHA=$ORDINARY_SHA run_case "changed code is held" 0
cmp -s "$PRIOR_INIT" "$WORK_INIT" || fail "held: the prior release's bytes did not ship"
grep -q "held: code changed" "$TMP_DIR/out.txt" || fail "held: no hold message"
want_source "held" "$PRIOR_SHA"

# A recorded source for these exact bytes carries the hold age forward.
jq -n --arg d "sha256:$(sha256sum "$PRIOR_INIT" | awk '{print $1}')" --arg s "$ANCIENT_SHA" \
	'{format_version: 1, digest: $d, source_sha: $s}' >"$FIXTURE/guest-init.json"
RUN_SHA=$ORDINARY_SHA run_case "held code past the limit ships" 3
cmp -s "$HEAD_INIT" "$WORK_INIT" || fail "aged out: the head build was replaced"
grep -q "h old (limit 72 h)" "$TMP_DIR/out.txt" || fail "aged out: no age reason"
want_source "aged out" "$ORDINARY_SHA"

# A recorded source for other bytes is ignored: the prior release's commit is
# the source, and that is recent.
jq -n --arg s "$ANCIENT_SHA" '{format_version: 1, digest: "sha256:00", source_sha: $s}' >"$FIXTURE/guest-init.json"
RUN_SHA=$ORDINARY_SHA run_case "a provenance for other bytes is ignored" 0
want_source "mismatched provenance" "$PRIOR_SHA"
rm -f "$FIXTURE/guest-init.json"

RUN_SHA=$GUEST_SHA run_case "a guest-init source change ships" 3
grep -q "guest code changed outside the held tables: guest/init/secrets_linux.go )" "$TMP_DIR/out.txt" ||
	fail "guest change: no reason naming only the non-test file"
want_source "guest change" "$GUEST_SHA"

# A closure that cannot be listed ships rather than holds.
(
	export GUEST_INIT_CLOSURE=
	unset GUEST_INIT_CLOSURE
	RUN_SHA=$ORDINARY_SHA run_case "an unlistable closure (no go.mod here) ships" 3
	grep -q "closure could not be listed" "$TMP_DIR/out.txt" || fail "unlistable closure: no reason"
)

RUN_SHA=$TRAILER_SHA run_case "a Guest-Init-Ship trailer ships" 3
grep -q "Guest-Init-Ship: now" "$TMP_DIR/out.txt" || fail "trailer: no reason"

# Same code still reuses with the train on.
make_init "$HEAD_INIT" head 90
RUN_SHA=$ORDINARY_SHA run_case "same code with the train on" 0
want_source "same code, train on" "$PRIOR_SHA"
unset GUEST_INIT_HOLD_HOURS GUEST_INIT_CLOSURE

make_init "$HEAD_INIT" head 90

# --- No prior signed release (the tag has no release assets).
(
	export FAKE_ASSETS=""
	run_case "no prior signed release" 3
	want_source "no prior release" "$HEAD_SHA"
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
