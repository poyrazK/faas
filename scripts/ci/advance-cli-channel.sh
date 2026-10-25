#!/usr/bin/env bash
# Moves the curl installer's cli-channel pointers to RELEASE_TAG
# (docs/adr/20261025092617-cli-install-channel-pointer.md): CLI-LATEST for
# every release, CLI-STABLE for tags without a prerelease suffix, each only
# forward. release.yml runs this after the tag's CLI assets are attached, so
# a pointer never names a release the installer cannot download and verify.
set -euo pipefail

repo="${GITHUB_REPOSITORY:?}"
tag="${RELEASE_TAG:?}"
channel="${CLI_CHANNEL_RELEASE:-cli-channel}"
order="$(cd "$(dirname "$0")" && pwd)/cli_channel.py"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

python3 "$order" newer "$tag" >/dev/null || {
	echo "advance-cli-channel: $tag is not a release tag" >&2
	exit 1
}

if ! gh release view "$channel" --repo "$repo" >/dev/null 2>&1; then
	# A prerelease that is never "latest": GitHub's releases/latest, which the
	# installer's API fallback reads, must keep naming real releases.
	gh release create "$channel" --repo "$repo" --prerelease --latest=false \
		--title "CLI install channel" \
		--notes "Version pointers read by https://get.gregale.dev (ADR-20261025092617). Not a release: install with the curl command in any release's notes."
fi

advance() { # <pointer>
	local pointer="$1" current=""
	current="$(gh release download "$channel" --repo "$repo" --pattern "$pointer" --output - 2>/dev/null |
		head -n 1 | tr -d '[:space:]')" || current=""
	if ! python3 "$order" newer "$tag" "$current"; then
		echo "$pointer stays at $current ($tag is not newer)"
		return 0
	fi
	printf '%s\n' "$tag" >"$work/$pointer"
	gh release upload "$channel" "$work/$pointer" --repo "$repo" --clobber
	echo "$pointer: ${current:-none} -> $tag"
}

advance CLI-LATEST
if python3 "$order" stable "$tag"; then
	advance CLI-STABLE
fi
