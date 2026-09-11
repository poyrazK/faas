#!/bin/sh
# scripts/install.sh — one-line installer for the gregale CLI (ADR-172).
#
#   curl -fsSL https://get.gregale.dev | sh
#   curl -fsSL https://get.gregale.dev | sh -s -- --version v0.1.18 --dir /usr/local/bin
#
# Deliberately POSIX sh, not bash: this runs on whatever /bin/sh the user
# has, including dash on Debian images and the ancient bash 3.2 that ships
# as /bin/sh on macOS. No arrays, no [[, no local, no process substitution.
#
# The version is resolved at run time from the GitHub API rather than baked
# in, so a new release tag is live in this installer the moment its assets
# exist — there is no publish step for this channel (ADR-172 "Why").
#
# Every download is verified against the release's own CLI-SHA256SUMS before
# anything is placed on PATH. A checksum failure aborts with no install.

set -eu

REPO="${GREGALE_REPO:-poyrazK/faas}"
API="${GREGALE_API_BASE:-https://api.github.com}"
DOWNLOAD_BASE="${GREGALE_DOWNLOAD_BASE:-https://github.com/${REPO}/releases/download}"
BIN_NAME="gregale"

version="${GREGALE_VERSION:-}"
install_dir="${GREGALE_INSTALL_DIR:-}"

log() { printf '%s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

usage() {
	cat >&2 <<EOF
Install the gregale CLI.

Usage:
  curl -fsSL https://get.gregale.dev | sh
  curl -fsSL https://get.gregale.dev | sh -s -- [flags]

Flags:
  -v, --version <tag>   Install this exact release tag (e.g. v0.1.18).
                        Default: newest stable release.
  -d, --dir <path>      Install into this directory.
                        Default: \$HOME/.local/bin (or /usr/local/bin as root).
  -h, --help            Show this help.

Environment:
  GREGALE_VERSION       Same as --version.
  GREGALE_INSTALL_DIR   Same as --dir.
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
	-v | --version)
		[ "$#" -ge 2 ] || die "$1 needs a value"
		version="$2"
		shift 2
		;;
	-d | --dir)
		[ "$#" -ge 2 ] || die "$1 needs a value"
		install_dir="$2"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*) die "unknown flag: $1 (try --help)" ;;
	esac
done

# ---------------------------------------------------------------- platform

detect_os() {
	os="$(uname -s)"
	case "$os" in
	Darwin) printf 'darwin' ;;
	Linux) printf 'linux' ;;
	MINGW* | MSYS* | CYGWIN* | Windows_NT)
		# Not an oversight: cmd/gregale transitively imports pkg/fcvm,
		# which is unix-only (syscall.Stat_t / Mkfifo / SYS_IOCTL). See
		# ADR-172 "Consequences".
		die "Windows is not supported by the gregale CLI yet"
		;;
	*) die "unsupported operating system: $os" ;;
	esac
}

detect_arch() {
	arch="$(uname -m)"
	case "$arch" in
	x86_64 | amd64) printf 'amd64' ;;
	arm64 | aarch64) printf 'arm64' ;;
	*) die "unsupported architecture: $arch (gregale ships amd64 and arm64)" ;;
	esac
}

# ---------------------------------------------------------------- download

have() { command -v "$1" >/dev/null 2>&1; }

# fetch <url> <dest>. Exits non-zero on any HTTP error so a 404 on a
# mistyped tag surfaces as a failed install, not an empty file.
fetch() {
	if have curl; then
		curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1"
	elif have wget; then
		wget -q -O "$2" "$1"
	else
		die "need curl or wget on PATH"
	fi
}

# fetch_stdout <url>. Same, to stdout, tolerating failure (callers that
# probe an endpoint check for empty output instead).
fetch_stdout() {
	if have curl; then
		curl -fsSL --proto '=https' --tlsv1.2 "$1" 2>/dev/null || true
	elif have wget; then
		wget -q -O - "$1" 2>/dev/null || true
	else
		die "need curl or wget on PATH"
	fi
}

# tag_from_release_json reads the first "tag_name" out of a GitHub
# releases payload. Deliberately jq-free: jq is not installable from
# inside a one-line installer.
tag_from_release_json() {
	sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

resolve_version() {
	# /releases/latest is GitHub's newest NON-prerelease, and 404s when
	# every release is a prerelease. That is exactly the state this repo
	# is in today (every tag so far is -rc.N), so fall back to the newest
	# release of any kind rather than leaving the installer dead — but say
	# so, because handing someone a release candidate silently is worse
	# than the extra line of output.
	tag="$(fetch_stdout "${API}/repos/${REPO}/releases/latest" | tag_from_release_json)"
	if [ -n "$tag" ]; then
		printf '%s' "$tag"
		return 0
	fi

	tag="$(fetch_stdout "${API}/repos/${REPO}/releases?per_page=1" | tag_from_release_json)"
	[ -n "$tag" ] || die "could not resolve a release from ${API}/repos/${REPO}/releases (pass --version)"
	log "warning: no stable release yet; installing prerelease ${tag}"
	printf '%s' "$tag"
}

# sha256_of <file> prints the lowercase hex digest.
sha256_of() {
	if have sha256sum; then
		sha256sum "$1" | cut -d' ' -f1
	elif have shasum; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "need sha256sum or shasum on PATH to verify the download"
	fi
}

# ---------------------------------------------------------------- install

os="$(detect_os)"
arch="$(detect_arch)"

[ -n "$version" ] || version="$(resolve_version)"
# Accept both "v0.1.18" and "0.1.18"; the asset names carry the bare
# semver while the tag carries the v.
case "$version" in
v*) tag="$version" ;;
*) tag="v$version" ;;
esac
semver="${tag#v}"

if [ -z "$install_dir" ]; then
	if [ "$(id -u)" = "0" ]; then
		install_dir="/usr/local/bin"
	else
		install_dir="$HOME/.local/bin"
	fi
fi

archive="${BIN_NAME}_${semver}_${os}_${arch}.tar.gz"
archive_url="${DOWNLOAD_BASE}/${tag}/${archive}"
sums_url="${DOWNLOAD_BASE}/${tag}/CLI-SHA256SUMS"
legacy_sums_url="${DOWNLOAD_BASE}/${tag}/SHA256SUMS"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/gregale-install.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

log "installing gregale ${tag} (${os}/${arch})"

fetch "$archive_url" "$tmp/$archive" ||
	die "download failed: $archive_url (is ${tag} a real release with ${os}/${arch} assets?)"
if ! fetch "$sums_url" "$tmp/CLI-SHA256SUMS"; then
	# Releases published before the CLI/daemon checksum assets were split
	# used SHA256SUMS for the CLI. Keep explicit old-version installs working
	# without allowing a current daemon checksum file to replace the CLI file.
	log "warning: ${tag} has no CLI-SHA256SUMS; trying the legacy SHA256SUMS asset"
	fetch "$legacy_sums_url" "$tmp/CLI-SHA256SUMS" ||
		die "download failed: $sums_url (legacy fallback also unavailable)"
fi

expected="$(sed -n "s/^\([0-9a-f]\{64\}\)[[:space:]]\{1,\}[*]\{0,1\}${archive}$/\1/p" "$tmp/CLI-SHA256SUMS" | head -n 1)"
[ -n "$expected" ] || die "CLI-SHA256SUMS for ${tag} has no entry for ${archive}"
actual="$(sha256_of "$tmp/$archive")"
if [ "$expected" != "$actual" ]; then
	die "checksum mismatch for ${archive}
  expected ${expected}
  actual   ${actual}
Refusing to install. Re-run, and if it persists open an issue — this means
the release asset and its CLI-SHA256SUMS disagree."
fi

tar -xzf "$tmp/$archive" -C "$tmp" ||
	die "could not extract $archive"
[ -f "$tmp/$BIN_NAME" ] || die "$archive did not contain a $BIN_NAME binary"

mkdir -p "$install_dir" || die "could not create $install_dir"
# install(1) over cp: atomic-ish replace, and it sets the mode in one
# call. A running gregale keeps its open inode, so upgrading underneath
# a long `gregale logs -f` does not break it.
if ! install -m 0755 "$tmp/$BIN_NAME" "$install_dir/$BIN_NAME" 2>/dev/null; then
	die "could not write $install_dir/$BIN_NAME (try --dir \$HOME/.local/bin, or re-run with sudo)"
fi

log "installed $install_dir/$BIN_NAME"

# PATH advice. Checked against the padded PATH so /usr/local/bin does not
# match a substring of /usr/local/binary-ish directories.
case ":$PATH:" in
*":$install_dir:"*) ;;
*)
	log ""
	log "warning: $install_dir is not on your PATH. Add it:"
	case "${SHELL##*/}" in
	zsh) log "  echo 'export PATH=\"$install_dir:\$PATH\"' >> ~/.zshrc && exec zsh" ;;
	fish) log "  fish_add_path $install_dir" ;;
	*) log "  echo 'export PATH=\"$install_dir:\$PATH\"' >> ~/.bashrc && exec bash" ;;
	esac
	log ""
	;;
esac

"$install_dir/$BIN_NAME" version >&2 || log "warning: installed binary did not run cleanly"

log ""
log "next: gregale login    (shell completion: gregale completion --help)"
