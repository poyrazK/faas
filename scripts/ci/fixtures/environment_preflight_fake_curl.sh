#!/usr/bin/env bash
set -euo pipefail

args=("$@")
for arg in "${args[@]}"; do
	if [[ "$arg" == "--get" ]]; then
		printf '{"value":"fake-github-id-token"}'
		exit 0
	fi
done

output_file=""
body=""
for ((index = 0; index < ${#args[@]}; index++)); do
	case "${args[$index]}" in
		--output) output_file="${args[$((index + 1))]}" ;;
		--data-binary) body="${args[$((index + 1))]}" ;;
	esac
done
[[ -n "$output_file" ]] || exit 22
printf '%s' "$body" >"$ACTION_TEST_EXCHANGE_REQUEST"
: >"$ACTION_TEST_CURL_SEEN"
if [[ "${ACTION_TEST_BAD_SCOPES:-false}" == "true" ]]; then
	jq -n '{bearer:"fp_oidc_0123456789abcdef0123456789abcdef0123456789abcdef",scopes:["deploy:write"]}' >"$output_file"
else
	jq -n '{bearer:"fp_oidc_0123456789abcdef0123456789abcdef0123456789abcdef",scopes:["project_environments:read","project_environments:qualify"]}' >"$output_file"
fi
printf '200'
