#!/usr/bin/env bash
# scripts/archive-cli-binary.sh — package one built gregale binary into a
# reproducible release archive (ADR-172).
#
# Extracted from release.yml so the reproducibility claim is testable:
# archive-cli-binary_test.sh runs this twice and asserts byte equality.
# While the command lived inline in the workflow, the claim was only a
# comment — and it was wrong, because `tar -czf` writes the current
# wall-clock time into the gzip header's MTIME field. `--mtime` normalizes
# the tar entries, not the gzip wrapper, so two runs a second apart
# produced different bytes.
#
# Determinism here comes from four things:
#   --sort=name          stable entry order
#   --owner/--group/--numeric-owner   no builder identity in the archive
#   --mtime              fixed entry timestamps (pass the commit time)
#   gzip -n              no timestamp or filename in the gzip header
# plus --format=gnu, so a different GNU tar default cannot change the
# layout underneath us.
set -euo pipefail

binary=""
output=""
mtime=""

die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat >&2 <<EOF
Usage: $0 --binary <path> --output <file.tar.gz> --mtime <@epoch>

  --binary   Built gregale binary. Archived as the single entry "gregale".
  --output   Destination .tar.gz.
  --mtime    Fixed timestamp for the entry, e.g. "@1757000000"
             (release.yml passes the commit time).
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
	--binary)
		binary="${2:-}"
		shift 2
		;;
	--output)
		output="${2:-}"
		shift 2
		;;
	--mtime)
		mtime="${2:-}"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*) die "unknown flag: $1" ;;
	esac
done

[ -n "$binary" ] || {
	usage
	die "--binary is required"
}
[ -n "$output" ] || {
	usage
	die "--output is required"
}
[ -n "$mtime" ] || {
	usage
	die "--mtime is required"
}
[ -f "$binary" ] || die "no such binary: $binary"

# BSD tar (the default on macOS) has no --sort, so a developer running this
# locally would silently get a non-reproducible archive. Require GNU tar and
# prefer Homebrew's gtar when present.
if command -v gtar >/dev/null 2>&1; then
	TAR=gtar
elif tar --version 2>/dev/null | head -1 | grep -q 'GNU tar'; then
	TAR=tar
else
	die "GNU tar is required (macOS: brew install gnu-tar). BSD tar lacks --sort, which this archive's reproducibility depends on."
fi

command -v gzip >/dev/null 2>&1 || die "gzip is required"

workdir="$(mktemp -d "${TMPDIR:-/tmp}/gregale-archive.XXXXXX")"
trap 'rm -rf "$workdir"' EXIT

# The archive must contain exactly one entry named "gregale", whatever the
# built file was called on disk: install.sh and the npm staging script both
# extract that name.
install -m 0755 "$binary" "$workdir/gregale"

mkdir -p "$(dirname "$output")"
"$TAR" \
	--format=gnu \
	--sort=name \
	--owner=0 --group=0 --numeric-owner \
	--mtime="$mtime" \
	-cf - -C "$workdir" gregale |
	gzip -n >"$output"

printf 'archived %s -> %s\n' "$binary" "$output"
