#!/usr/bin/env bash
# Run a qualification and server-side promotion preview as one CI gate.
# Never enable xtrace: OIDC assertions and Gregale bearers stay in memory.
set -euo pipefail
umask 077

ACTION_PATH="${ACTION_PATH:-$(cd "$(dirname "$0")/.." && pwd)}"
CLI="$ACTION_PATH/../deploy/bin/gregale"
VERSION_FILE="$ACTION_PATH/../deploy/src/version.txt"
API_BASE="${INPUT_API_BASE:-https://api.gregale.dev}"
while [[ "$API_BASE" == */ ]]; do API_BASE="${API_BASE%/}"; done
tmp_dir="$(mktemp -d)"
FAAS_TOKEN=""
oidc_jwt=""
trap 'unset FAAS_TOKEN oidc_jwt; rm -rf "$tmp_dir"' EXIT

fail() {
	echo "::error::$1" >&2
	if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
		printf 'status=failed\n' >>"$GITHUB_OUTPUT"
	fi
	exit 1
}

if [[ -z "${INPUT_PROJECT:-}" || -z "${INPUT_FROM:-}" || -z "${INPUT_TO:-}" || -z "${INPUT_PROFILE:-}" ]]; then
	fail "project, from, to, and profile inputs are required"
fi
if [[ ! "${INPUT_PROJECT}" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]]; then
	fail "project must be a Gregale project slug"
fi
if [[ ! "${INPUT_FROM}" =~ ^[a-z0-9]([a-z0-9-]{0,31}[a-z0-9])?$ ||
	  ! "${INPUT_TO}" =~ ^[a-z0-9]([a-z0-9-]{0,31}[a-z0-9])?$ ||
	  "${INPUT_FROM}" == "${INPUT_TO}" || "${INPUT_FROM}" == "default" || "${INPUT_TO}" == "default" ]]; then
	fail "from and to must be different registered environment slugs"
fi
if [[ "${INPUT_SYNC_CONFIG:-false}" != "true" && "${INPUT_SYNC_CONFIG:-false}" != "false" ]]; then
	fail "sync-config must be true or false"
fi
if [[ "$API_BASE" != https://* && ! "$API_BASE" =~ ^http://(localhost|127\.0\.0\.1|\[::1\])(:[0-9]+)?(/|$) ]]; then
	fail "api-base must use HTTPS (HTTP is permitted only for localhost)"
fi
if [[ ! -x "$CLI" || ! -f "$VERSION_FILE" ]]; then
	fail "bundled Gregale CLI is missing; use a tagged Action release"
fi
if ! command -v jq >/dev/null 2>&1 || ! command -v curl >/dev/null 2>&1; then
	fail "jq and curl are required on the GitHub Actions runner"
fi
if [[ -n "${GITHUB_WORKSPACE:-}" ]]; then
	cd "$GITHUB_WORKSPACE"
fi
if [[ ! -r "${INPUT_PROFILE}" ]]; then
	fail "qualification profile is not readable: ${INPUT_PROFILE}"
fi

cli_version="$(<"$VERSION_FILE")"
echo "gregale CLI version: $cli_version"

if [[ -n "${INPUT_API_KEY:-}" ]]; then
	FAAS_TOKEN="$INPUT_API_KEY"
else
	if [[ -z "${ACTIONS_ID_TOKEN_REQUEST_URL:-}" || -z "${ACTIONS_ID_TOKEN_REQUEST_TOKEN:-}" ]]; then
		fail "authentication unavailable: pass api-key or grant permissions: id-token: write"
	fi
	if [[ -z "${INPUT_OIDC_AUDIENCE:-}" ]]; then
		fail "oidc-audience must not be empty"
	fi
	if ! oidc_jwt="$(curl --fail --silent --show-error --get \
		-H "Authorization: Bearer ${ACTIONS_ID_TOKEN_REQUEST_TOKEN}" \
		--data-urlencode "audience=${INPUT_OIDC_AUDIENCE}" \
		"$ACTIONS_ID_TOKEN_REQUEST_URL" | jq -er '.value | strings | select(length > 0)')"; then
		fail "could not request a GitHub Actions OIDC assertion"
	fi
	exchange_body="$(jq -n --arg token "$oidc_jwt" --arg aud "$INPUT_OIDC_AUDIENCE" \
		'{provider:"github",token:$token,aud:$aud,capability:"environment-preflight"}')"
	http_status="$(curl --silent --show-error --output "$tmp_dir/exchange.json" --write-out '%{http_code}' \
		-H 'Content-Type: application/json' -H 'Accept: application/json' \
		--data-binary "$exchange_body" "${API_BASE}/v1/auth/oidc/exchange" 2>/dev/null || true)"
	unset oidc_jwt exchange_body
	if [[ "$http_status" != 2?? ]] || ! jq -e '
		(.scopes | sort) == ["project_environments:qualify", "project_environments:read"] and
		(.bearer | type == "string" and test("^fp_oidc_[0-9a-f]{48}$"))
	' "$tmp_dir/exchange.json" >/dev/null 2>&1; then
		fail "Gregale did not issue the expected environment-preflight-only bearer; verify the GitHub OIDC trust binding"
	fi
	FAAS_TOKEN="$(jq -er '.bearer' "$tmp_dir/exchange.json")"
fi

args=(projects environments preflight "$INPUT_PROJECT" --from "$INPUT_FROM" --to "$INPUT_TO" --profile "$INPUT_PROFILE" --json)
if [[ "${INPUT_SYNC_CONFIG:-false}" == "true" ]]; then
	args+=(--sync-config)
fi
set +e
FAAS_API="$API_BASE" FAAS_TOKEN="$FAAS_TOKEN" FAAS_JSON=1 "$CLI" "${args[@]}" >"$tmp_dir/result.json" 2>"$tmp_dir/cli.stderr"
cli_status=$?
set -e
if ! jq -e 'type == "object" and (.status == "ready" or .status == "blocked")' "$tmp_dir/result.json" >/dev/null 2>&1; then
	if [[ -s "$tmp_dir/cli.stderr" ]]; then
		sed -E \
			-e 's/fp_oidc_[[:xdigit:]]{48}/[REDACTED]/g' \
			-e 's/(Bearer )[A-Za-z0-9._-]+/\1[REDACTED]/g' \
			-e 's/FAAS_TOKEN=[^[:space:]]+/FAAS_TOKEN=[REDACTED]/g' \
			"$tmp_dir/cli.stderr" >&2
	fi
	fail "Gregale environment preflight did not return a qualification result"
fi

status="$(jq -r '.status' "$tmp_dir/result.json")"
qualification_id="$(jq -r '.qualification.id // empty' "$tmp_dir/result.json")"
can_promote="$(jq -r '.promotion_preview.can_promote // false' "$tmp_dir/result.json")"
approval_required="$(jq -r '.promotion_preview.approval_required // false' "$tmp_dir/result.json")"
blocking_reasons="$(jq -c '.blocking_reasons // []' "$tmp_dir/result.json")"
[[ "$qualification_id" =~ ^[A-Za-z0-9-]+$ ]] || qualification_id=""
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
	{
		printf 'status=%s\n' "$status"
		printf 'qualification-id=%s\n' "$qualification_id"
		printf 'can-promote=%s\n' "$can_promote"
		printf 'approval-required=%s\n' "$approval_required"
		printf 'blocking-reasons=%s\n' "$blocking_reasons"
		printf 'cli-version=%s\n' "$cli_version"
	} >> "$GITHUB_OUTPUT"
fi

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
	{
		echo '### Gregale environment promotion preflight'
		echo
		echo "- **Project:** \`${INPUT_PROJECT}\`"
		echo "- **Environments:** \`${INPUT_FROM}\` → \`${INPUT_TO}\`"
		echo "- **Result:** **${status}**"
		echo "- **Qualification:** \`${qualification_id:-unavailable}\`"
		echo "- **Promotion allowed:** \`${can_promote}\`"
		echo "- **Separate approval required:** \`${approval_required}\`"
		jq -r '.blocking_reasons[]? | gsub("[\\r\\n]"; " ") | "- **Blocker:** " + @html' "$tmp_dir/result.json"
	} >> "$GITHUB_STEP_SUMMARY"
fi

if [[ "$cli_status" -ne 0 || "$status" != "ready" || "$can_promote" != "true" ]]; then
	# The qualification may pass while promotion remains blocked by policy,
	# release drift, or a required approval. The step deliberately fails closed.
	exit 1
fi
