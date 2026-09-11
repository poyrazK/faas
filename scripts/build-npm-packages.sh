#!/usr/bin/env bash
# scripts/build-npm-packages.sh — stage the npm channel's five packages
# from the release archives (ADR-172).
#
# Produces, under --out-dir:
#
#   gregale/                       root launcher package (publish LAST)
#   @gregale/cli-darwin-amd64/     binary packages (publish FIRST)
#   @gregale/cli-darwin-arm64/
#   @gregale/cli-linux-amd64/
#   @gregale/cli-linux-arm64/
#   publish-order.txt              newline-separated dirs, in publish order
#
# Publish order matters: the root package pins its optionalDependencies to
# an exact version, so publishing it first leaves a window where
# `npm install -g gregale` resolves nothing installable.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMPLATE_DIR="$REPO_ROOT/packaging/npm"

version=""
artifacts_dir=""
out_dir=""

die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat >&2 <<EOF
Usage: $0 --version <vX.Y.Z> --artifacts-dir <dir> --out-dir <dir>

  --version        Release tag. A leading "v" is stripped for the npm
                   version, which must be valid semver.
  --artifacts-dir  Directory holding gregale_<semver>_<os>_<arch>.tar.gz.
  --out-dir        Staging directory to create. Must not already exist.
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
	--version)
		version="${2:-}"
		shift 2
		;;
	--artifacts-dir)
		artifacts_dir="${2:-}"
		shift 2
		;;
	--out-dir)
		out_dir="${2:-}"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*) die "unknown flag: $1" ;;
	esac
done

[ -n "$version" ] || {
	usage
	die "--version is required"
}
[ -n "$artifacts_dir" ] || {
	usage
	die "--artifacts-dir is required"
}
[ -n "$out_dir" ] || {
	usage
	die "--out-dir is required"
}
[ -d "$artifacts_dir" ] || die "--artifacts-dir does not exist: $artifacts_dir"

semver="${version#v}"
# npm rejects an invalid version at publish time, which is the worst place
# to find out. Validate the shape the release pipeline actually produces:
# X.Y.Z with an optional prerelease suffix (0.1.18-rc.119).
[[ "$semver" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] ||
	die "not a publishable semver: $semver (from --version $version)"

[ ! -e "$out_dir" ] || die "--out-dir already exists: $out_dir"
mkdir -p "$out_dir"

# render <template> <dest> <key=value>...
render() {
	local template="$1" dest="$2"
	shift 2
	local body
	body="$(cat "$template")"
	local pair key value
	for pair in "$@"; do
		key="${pair%%=*}"
		value="${pair#*=}"
		body="${body//__${key}__/$value}"
	done
	printf '%s' "$body" >"$dest"
	# A leftover placeholder means a template gained a field the renderer
	# does not fill. Fail here rather than publishing "__VERSION__" to npm.
	if grep -q '__[A-Z_]\{2,\}__' "$dest"; then
		die "unsubstituted placeholder left in $dest: $(grep -o '__[A-Z_]\{2,\}__' "$dest" | sort -u | tr '\n' ' ')"
	fi
}

publish_order="$out_dir/publish-order.txt"
: >"$publish_order"

# go_os go_arch npm_os npm_cpu — npm names the architectures differently
# from Go (x64 vs amd64), so both vocabularies are spelled out per target.
targets=(
	"darwin amd64 darwin x64"
	"darwin arm64 darwin arm64"
	"linux amd64 linux x64"
	"linux arm64 linux arm64"
)

for target in "${targets[@]}"; do
	read -r go_os go_arch npm_os npm_cpu <<<"$target"

	pkg_name="@gregale/cli-${go_os}-${go_arch}"
	pkg_dir="$out_dir/@gregale/cli-${go_os}-${go_arch}"
	archive="$artifacts_dir/gregale_${semver}_${go_os}_${go_arch}.tar.gz"

	[ -f "$archive" ] || die "missing release archive: $archive"

	mkdir -p "$pkg_dir/bin"
	tar -xzf "$archive" -C "$pkg_dir/bin" gregale ||
		die "$archive does not contain a 'gregale' entry"
	chmod 0755 "$pkg_dir/bin/gregale"

	render "$TEMPLATE_DIR/platform/package.json.tmpl" "$pkg_dir/package.json" \
		"PKG_NAME=$pkg_name" \
		"VERSION=$semver" \
		"GO_OS=$go_os" \
		"GO_ARCH=$go_arch" \
		"NPM_OS=$npm_os" \
		"NPM_CPU=$npm_cpu"

	cp "$REPO_ROOT/LICENSE" "$pkg_dir/LICENSE"
	printf '%s\n' "$pkg_dir" >>"$publish_order"
	printf 'staged %s\n' "$pkg_name"
done

# Root launcher package last, so publish-order.txt is also publish-safe
# when consumed top to bottom.
root_dir="$out_dir/gregale"
mkdir -p "$root_dir/bin"
cp "$TEMPLATE_DIR/cli/bin/gregale.js" "$root_dir/bin/gregale.js"
chmod 0755 "$root_dir/bin/gregale.js"
cp "$TEMPLATE_DIR/cli/README.md" "$root_dir/README.md"
cp "$REPO_ROOT/LICENSE" "$root_dir/LICENSE"
render "$TEMPLATE_DIR/cli/package.json.tmpl" "$root_dir/package.json" \
	"VERSION=$semver"
printf '%s\n' "$root_dir" >>"$publish_order"
printf 'staged gregale (root launcher)\n'

printf 'npm packages staged in %s for version %s\n' "$out_dir" "$semver"
