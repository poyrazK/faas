#!/usr/bin/env bash
# src/run.sh — Gregale deploy action runner (issue #270 / ADR-093).
#
# Two sub-commands:
#   validate — checks that api-key is non-empty and app is non-empty.
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

cmd_validate() {
    if [ -z "${INPUT_APP:-}" ]; then
        die "missing required input: app"
    fi
    # api-key is masked by Actions automatically; we just sanity-check
    # that the env propagation worked.
    if [ -z "${FAAS_TOKEN:-}" ]; then
        die "missing required input: api-key (FAAS_TOKEN is unset)"
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

cmd_deploy() {
    cmd_validate

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

    # 4. Optionally wait. The vendored CLI tails the SSE build log
    #    when --wait is set; we use a separate mode here so the
    #    failure path stays distinct (cancelled / timeout vs failed).
    if [ "${INPUT_WAIT:-true}" = "true" ]; then
        local timeout="${INPUT_WAIT_TIMEOUT:-600}"
        if ! "$BIN" deployment "$dep_id" --wait --json --timeout "$timeout" 2>&1; then
            # Annotate distinguishes cancelled/timeout from failed.
            local status="failed"
            echo "status=$status" >> "$GITHUB_OUTPUT"
            die "deployment $dep_id did not become ready within ${timeout}s"
        fi
    fi

    # 5. Final state.
    echo "status=ready" >> "$GITHUB_OUTPUT"
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
