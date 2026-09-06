#!/usr/bin/env bash
# src/run.sh — Gregale deploy action runner (issue #270 / ADR-093).
#
# Two sub-commands:
#   validate — checks authentication inputs, app, format, and bundled CLI.
#   deploy   — invokes the vendored gregale binary, optionally waits
#              for the deployment to settle, writes outputs to
#              $GITHUB_OUTPUT.
#
# SECURITY: set -euo pipefail only — never -x. Anything routed to
# GITHUB_OUTPUT or ::error is grep-clean of the bearer token. The
# token is set via FAAS_TOKEN env, which the vendored CLI reads via
# pkg/api/client.go:199-201 (Authorization: Bearer header). The
# token never appears in stdout or stderr of gregale itself.
#
# The token never appears in $GITHUB_OUTPUT either — that channel
# propagates customer-output identifiers (deployment_id, status, url)
# only. The api-key input is consumed inside the run step's env block
# and dropped.

set -euo pipefail

# Resolve the action path so the script works whether invoked from
# ${{ github.action_path }} or directly (tests).
ACTION_PATH="${ACTION_PATH:-$(cd "$(dirname "$0")/.." && pwd)}"
BIN="$ACTION_PATH/bin/gregale"
VERSION_FILE="$ACTION_PATH/src/version.txt"

# Helper: surface a GitHub Actions error and exit.
# Usage: die "message"
die() {
    echo "::error::$1" >&2
    exit 1
}

# GitHub-native visibility is deliberately best-effort. A customer may use a
# token that cannot write Checks, or run the action outside GitHub Actions; in
# either case the Gregale deployment must keep working and the deployment URL
# remains available through the action output.
write_step_summary() {
	local status="$1" dep_id="$2" url="$3"
	if [ -z "${GITHUB_STEP_SUMMARY:-}" ]; then
		return
	fi
	{
		echo "### Gregale deployment"
		echo
		echo "- **Status:** \`$status\`"
		echo "- **Deployment:** [$dep_id]($url)"
		if [ "$status" = "queued" ]; then
			echo "- The workflow returned without waiting; follow the deployment link for live progress."
		fi
	} >> "$GITHUB_STEP_SUMMARY"
}

check_run_request() {
	local status="$1" conclusion="$2" dep_id="$3" url="$4" title="$5"
	local display_status="${6:-$status}"
	local repo="${GITHUB_REPOSITORY:-}" sha="${GITHUB_SHA:-}" token="${GITHUB_TOKEN:-}"
	local api_base="${GITHUB_API_URL:-https://api.github.com}"
	if [ -z "$repo" ] || [ -z "$sha" ] || [ -z "$token" ]; then
		return
	fi
	if ! command -v jq >/dev/null 2>&1; then
		echo "::warning::jq is unavailable; skipped GitHub Check Run publication" >&2
		return
	fi

	local payload response_file http_status
	if [ "$status" = "completed" ]; then
		payload="$(jq -n \
			--arg name "Gregale deployment" \
			--arg head_sha "$sha" \
			--arg status "$status" \
			--arg conclusion "$conclusion" \
			--arg details_url "$url" \
			--arg title "$title" \
			--arg summary "Deployment ${dep_id} is ${display_status}." \
			'{name:$name, head_sha:$head_sha, status:$status, conclusion:$conclusion, details_url:$details_url, output:{title:$title, summary:$summary}}')"
	else
		payload="$(jq -n \
		--arg name "Gregale deployment" \
		--arg head_sha "$sha" \
		--arg status "$status" \
		--arg details_url "$url" \
		--arg title "$title" \
		--arg summary "Deployment ${dep_id} is ${display_status}." \
		'{name:$name, head_sha:$head_sha, status:$status, details_url:$details_url, output:{title:$title, summary:$summary}}')"
	fi
	response_file="${RUNNER_TEMP:-/tmp}/gregale-check-run-${BASHPID}.json"
	http_status="$(curl --silent --show-error --output "$response_file" --write-out '%{http_code}' \
		--request POST \
		-H "Accept: application/vnd.github+json" \
		-H "Authorization: Bearer ${token}" \
		-H "X-GitHub-Api-Version: 2022-11-28" \
		-H "Content-Type: application/json" \
		--data "$payload" \
		"${api_base%/}/repos/${repo}/check-runs" 2>/dev/null || true)"
	if [[ "$http_status" != 2?? ]]; then
		echo "::warning::GitHub Check Run was not created (HTTP ${http_status:-unknown}); grant checks: write to enable it" >&2
		rm -f "$response_file"
		return
	fi
	GITHUB_CHECK_RUN_ID="$(jq -r '.id // empty' "$response_file")"
	rm -f "$response_file"
	if [ -n "$GITHUB_CHECK_RUN_ID" ]; then
		echo "check-run-id=$GITHUB_CHECK_RUN_ID" >> "$GITHUB_OUTPUT"
	fi
}

check_run_update() {
	local status="$1" conclusion="$2" dep_id="$3" url="$4" title="$5"
	local repo="${GITHUB_REPOSITORY:-}" token="${GITHUB_TOKEN:-}" check_id="${GITHUB_CHECK_RUN_ID:-}"
	local api_base="${GITHUB_API_URL:-https://api.github.com}"
	if [ -z "$repo" ] || [ -z "$token" ] || [ -z "$check_id" ] || ! command -v jq >/dev/null 2>&1; then
		return
	fi

	local payload
	payload="$(jq -n \
		--arg status "$status" \
		--arg conclusion "$conclusion" \
		--arg details_url "$url" \
		--arg title "$title" \
		--arg summary "Deployment ${dep_id} is ${status}." \
		'{status:$status, conclusion:$conclusion, details_url:$details_url, output:{title:$title, summary:$summary}}')"
	local http_status
	http_status="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
		--request PATCH \
		-H "Accept: application/vnd.github+json" \
		-H "Authorization: Bearer ${token}" \
		-H "X-GitHub-Api-Version: 2022-11-28" \
		-H "Content-Type: application/json" \
		--data "$payload" \
		"${api_base%/}/repos/${repo}/check-runs/${check_id}" 2>/dev/null || true)"
	if [[ "$http_status" != 2?? ]]; then
		echo "::warning::GitHub Check Run ${check_id} could not be updated (HTTP ${http_status:-unknown})" >&2
	fi
}

cmd_validate() {
    if [ -z "${INPUT_APP:-}" ]; then
        die "missing required input: app"
    fi
	if [[ ! "${INPUT_APP}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
		die "invalid app slug: use letters, numbers, dot, underscore, or hyphen"
	fi
    if [ -z "${FAAS_TOKEN:-}" ]; then
		if [ -z "${ACTIONS_ID_TOKEN_REQUEST_URL:-}" ] || [ -z "${ACTIONS_ID_TOKEN_REQUEST_TOKEN:-}" ]; then
			die "authentication unavailable: pass api-key or grant permissions: id-token: write"
		fi
    fi
    if [ "${INPUT_FORMAT:-tarball}" != "tarball" ]; then
        die "unsupported input format: ${INPUT_FORMAT} (only tarball is currently supported)"
    fi
	if [ "${INPUT_WAIT:-true}" != "true" ] && [ "${INPUT_WAIT:-true}" != "false" ]; then
		die "wait must be true or false"
	fi
	if [[ ! "${INPUT_WAIT_TIMEOUT:-600}" =~ ^[0-9]+$ ]] || [ "${INPUT_WAIT_TIMEOUT:-600}" -le 0 ]; then
		die "wait-timeout must be a positive integer"
	fi
    if [ ! -x "$BIN" ]; then
        die "vendored binary not found at $BIN (action must be released as a tagged version)"
    fi
    if [ ! -f "$VERSION_FILE" ]; then
        die "version file not found at $VERSION_FILE (action must be released as a tagged version)"
    fi
    # Echo the bundled CLI version so the action log shows what's
    # actually running. Note: this is NOT the action version — it's
    # the gregale CLI version. Useful for post-mortem traceability.
    local cli_version
    cli_version="$(cat "$VERSION_FILE")"
    echo "gregale CLI version: $cli_version"
}

exchange_oidc() {
	local refresh="${1:-}"
	if [ "$refresh" != "refresh" ] && [ -n "${FAAS_TOKEN:-}" ]; then
		return
	fi
	if [ "$refresh" = "refresh" ]; then
		unset FAAS_TOKEN
	fi
	local audience="${INPUT_OIDC_AUDIENCE:-gregale}"
	if [[ ! "$audience" =~ ^[A-Za-z0-9._:/-]+$ ]]; then
		die "oidc-audience contains unsupported characters"
	fi
	local github_response github_jwt exchange_response bearer api_base
	if ! github_response="$(curl --fail --silent --show-error --get \
		-H "Authorization: Bearer ${ACTIONS_ID_TOKEN_REQUEST_TOKEN}" \
		--data-urlencode "audience=${audience}" \
		"${ACTIONS_ID_TOKEN_REQUEST_URL}")"; then
		die "could not obtain a GitHub Actions OIDC token"
	fi
	github_jwt="$(printf '%s' "$github_response" | grep -oE '"value"[[:space:]]*:[[:space:]]*"[A-Za-z0-9._-]+"' | head -1 | cut -d'"' -f4)"
	if [ -z "$github_jwt" ]; then
		die "GitHub OIDC response did not contain a token"
	fi
	api_base="${FAAS_API:-https://api.gregale.dev}"
	api_base="${api_base%/}"
	if ! exchange_response="$(curl --fail --silent --show-error \
		-H "Content-Type: application/json" \
		--data "{\"provider\":\"github\",\"token\":\"${github_jwt}\",\"aud\":\"${audience}\",\"app\":\"${INPUT_APP}\"}" \
		"${api_base}/v1/auth/oidc/exchange")"; then
		die "Gregale rejected the GitHub OIDC identity; verify the account trust policy"
	fi
	bearer="$(printf '%s' "$exchange_response" | grep -oE '"bearer"[[:space:]]*:[[:space:]]*"fp_oidc_[a-fA-F0-9]+"' | head -1 | cut -d'"' -f4)"
	if [ -z "$bearer" ]; then
		die "Gregale OIDC exchange did not return a bearer"
	fi
	export FAAS_TOKEN="$bearer"
	USING_OIDC=true
}

cmd_deploy() {
    cmd_validate
	exchange_oidc

    local cli_version
    cli_version="$(cat "$VERSION_FILE")"

    # 1. Surface the cli-version output so downstream steps can lint
    #    for drift. echo "key=value" >> "$GITHUB_OUTPUT" is the
    #    canonical mechanism.
    {
        echo "cli-version=$cli_version"
        echo "app-slug=${INPUT_APP}"
    } >> "$GITHUB_OUTPUT"

    # 2. Invoke the vendored CLI. The wire shape is the same as
    #    `gregale deploy --repo --ref` — POST /v1/apps/{slug}/deployments/source-ref.
    #    --json stdout is the canonical DeploymentResponse shape;
    #    stderr in failure mode is the RFC 7807 Problem JSON line
    #    (cmd/gregale/json_flag.go:116-122 writeJSONProblem).
    #
    #    Issue #977 / ADR-116: append --reason / --tag /
    #    --deployed-by / --pr-number only when the input is non-
    #    empty, so unset inputs (the common case for push events
    #    or first-time adopters) keep the pre-#977 wire shape byte-
    #    identical. --deployed-by defaults to ${{ github.actor }} on
    #    the action.yml side; --pr-number defaults to
    #    ${{ github.event.pull_request.number }} for PR events.
    #    The CLI's --tag validator rejects any out-of-set value
    #    with a clean exit 1 BEFORE the wire is touched.
    local annotation_args=()
    if [ -n "${INPUT_REASON:-}" ]; then
        annotation_args+=(--reason "$INPUT_REASON")
    fi
    if [ -n "${INPUT_TAG:-}" ]; then
        annotation_args+=(--tag "$INPUT_TAG")
    fi
    if [ -n "${INPUT_DEPLOYED_BY:-}" ]; then
        annotation_args+=(--deployed-by "$INPUT_DEPLOYED_BY")
    fi
    if [ -n "${INPUT_PR_NUMBER:-}" ]; then
        annotation_args+=(--pr-number "$INPUT_PR_NUMBER")
    fi
    local dep_json
    if ! dep_json="$(
        "$BIN" deploy --json \
            --name "$INPUT_APP" \
            --repo "$INPUT_REPO" \
            --ref "$INPUT_REF" \
            "${annotation_args[@]}" \
            2>&1
    )"; then
        # The CLI exited non-zero. Persist the captured output for
        # the annotate step. The bearer token is NEVER in this
        # output — pkg/api/client.go sets the Authorization header,
        # and the server doesn't echo it back.
        echo "$dep_json" > "${RUNNER_TEMP:-/tmp}/gregale-deploy-action.stderr"
        die "deploy failed (see annotations for details)"
    fi

    # 3. Extract the deployment id from the JSON response. We use a
    #    tiny grep-and-cut rather than jq to keep the dependency
    #    surface zero.
    local dep_id
    dep_id="$(printf '%s' "$dep_json" | grep -oE '"id"[[:space:]]*:[[:space:]]*"[0-9a-fA-F]{32}"' | head -1 | grep -oE '[0-9a-fA-F]{32}')"
    if [ -z "$dep_id" ]; then
        die "could not extract deployment id from CLI output: $dep_json"
    fi
    echo "deployment-id=$dep_id" >> "$GITHUB_OUTPUT"
    # Compose the deployment-record URL. We strip the trailing slash
    # from FAAS_API (or fall back to the api-base input default) so
    # the concatenation is robust to either form. The output is
    # informational — customers use it to link back to the
    # deployment row in the control-plane dashboard.
    local api_base="${FAAS_API:-https://api.gregale.dev}"
    api_base="${api_base%/}"
    echo "url=${api_base}/v1/apps/${INPUT_APP}/deployments/${dep_id}" >> "$GITHUB_OUTPUT"
	local deployment_url="${api_base}/v1/apps/${INPUT_APP}/deployments/${dep_id}"
	local check_status="queued"
	if [ "${INPUT_WAIT:-true}" = "true" ]; then
		check_status="in_progress"
	fi
	GITHUB_CHECK_RUN_ID=""
	if [ "$check_status" = "queued" ]; then
		# A non-blocking action cannot update the Check Run after the workflow
		# exits. Mark the request neutral and link to the live Gregale record
		# instead of leaving an indefinitely pending check.
		check_run_request "completed" "neutral" "$dep_id" "$deployment_url" "Gregale deployment queued" "queued"
	else
		check_run_request "in_progress" "" "$dep_id" "$deployment_url" "Gregale deployment in progress" "in_progress"
	fi
	write_step_summary "$check_status" "$dep_id" "$deployment_url"

    # 4. Optionally wait. The vendored CLI tails the SSE build log
    #    when --wait is set; we use a separate mode here so the
    #    failure path stays distinct (cancelled / timeout vs failed).
    if [ "${INPUT_WAIT:-true}" = "true" ]; then
        local timeout="${INPUT_WAIT_TIMEOUT:-600}"
        local wait_output="" started now remaining attempt_timeout wait_succeeded=false
		started="$(date +%s)"
		while true; do
			now="$(date +%s)"
			remaining=$((timeout - now + started))
			if [ "$remaining" -le 0 ]; then
				wait_output="Deployment wait timed out after ${timeout}s"
				break
			fi
			attempt_timeout="$remaining"
			# GitHub assertions and the derived bearer are intentionally short
			# lived. Poll in bounded slices and renew between slices so a slow
			# build can still use the customer-facing 10-minute default.
			if [ "${USING_OIDC:-false}" = "true" ] && [ "$attempt_timeout" -gt 240 ]; then
				attempt_timeout=240
			fi
			if wait_output="$("$BIN" --json deployment wait "$dep_id" --timeout "$attempt_timeout" 2>&1)"; then
				wait_succeeded=true
				break
			fi
			if ! printf '%s' "$wait_output" | grep -qi 'timed out'; then
				break
			fi
			if [ "${USING_OIDC:-false}" != "true" ]; then
				break
			fi
			exchange_oidc refresh
		done
		if [ "$wait_succeeded" != "true" ]; then
            local status="failed"
			if printf '%s' "$wait_output" | grep -qE '"status"[[:space:]]*:[[:space:]]*"cancelled"'; then
				status="cancelled"
			elif printf '%s' "$wait_output" | grep -qE '"status"[[:space:]]*:[[:space:]]*"superseded"'; then
				status="superseded"
			elif printf '%s' "$wait_output" | grep -qi 'timed out'; then
				status="timeout"
			fi
            echo "status=$status" >> "$GITHUB_OUTPUT"
			local conclusion="failure"
			case "$status" in
				cancelled) conclusion="cancelled" ;;
				timeout) conclusion="timed_out" ;;
				superseded) conclusion="stale" ;;
			esac
			check_run_update "completed" "$conclusion" "$dep_id" "$deployment_url" "Gregale deployment ${status}"
			write_step_summary "$status" "$dep_id" "$deployment_url"
			echo "$wait_output" > "${RUNNER_TEMP:-/tmp}/gregale-deploy-action.stderr"
			die "deployment $dep_id finished with status $status"
        fi
		echo "status=live" >> "$GITHUB_OUTPUT"
		check_run_update "completed" "success" "$dep_id" "$deployment_url" "Gregale deployment live"
		write_step_summary "live" "$dep_id" "$deployment_url"
	else
		echo "status=queued" >> "$GITHUB_OUTPUT"
    fi
}

case "${1:-}" in
    validate)
        cmd_validate
        ;;
    deploy)
        cmd_deploy
        ;;
    *)
        echo "usage: $0 {validate|deploy}" >&2
        exit 64
        ;;
esac
