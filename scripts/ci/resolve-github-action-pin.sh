#!/usr/bin/env bash
# Print the commit behind the maintained Action tag. Release builds embed this
# value so CLI workflow generation remains network-free.
set -euo pipefail

action_repository="poyrazK/faas"
action_version="v0"
tag_ref="refs/tags/$action_version"
remote="https://github.com/$action_repository.git"

refs="$(git ls-remote --exit-code "$remote" "$tag_ref" "${tag_ref}^{}")"
tag_sha=""
peeled_sha=""

while IFS=$'\t' read -r sha ref extra; do
	[[ -n "$sha" ]] || continue
	if [[ -n "${extra:-}" ]]; then
		printf 'invalid git tag response for %s\n' "$tag_ref" >&2
		exit 1
	fi

	case "$ref" in
		"$tag_ref")
			if [[ -n "$tag_sha" ]]; then
				printf 'duplicate git tag reference: %s\n' "$tag_ref" >&2
				exit 1
			fi
			tag_sha="$sha"
			;;
		"${tag_ref}^{}")
			if [[ -n "$peeled_sha" ]]; then
				printf 'duplicate peeled git tag reference: %s\n' "$tag_ref" >&2
				exit 1
			fi
			peeled_sha="$sha"
			;;
		*)
			continue
			;;
	esac

	if [[ ! "$sha" =~ ^[[:xdigit:]]{40}$ ]]; then
		printf 'invalid commit SHA for %s\n' "$ref" >&2
		exit 1
	fi
done <<< "$refs"

resolved_sha="${peeled_sha:-$tag_sha}"
if [[ -z "$resolved_sha" ]]; then
	printf 'git returned no %s tag reference\n' "$tag_ref" >&2
	exit 1
fi

printf '%s\n' "$resolved_sha" | tr '[:upper:]' '[:lower:]'
