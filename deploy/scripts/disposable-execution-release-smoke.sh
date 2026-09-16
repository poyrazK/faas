#!/usr/bin/env bash
# Verify that the production disposable-runs capability agrees with the
# execution admission path. The disabled path is read-only: apid must reject
# the probe before persisting anything. The enabled path runs a bounded,
# networkless Node 24 program and checks stdout retrieval.
set -euo pipefail
umask 077

: "${FAAS_TOKEN:?set FAAS_TOKEN to an eligible account bearer token}"

GREGALE_API_URL="${GREGALE_API_URL:-https://api.gregale.dev}"
SMOKE_TIMEOUT_SECONDS="${FAAS_EXECUTION_SMOKE_TIMEOUT_SECONDS:-90}"
[[ "$SMOKE_TIMEOUT_SECONDS" =~ ^[1-9][0-9]*$ ]] || {
	echo "FAAS_EXECUTION_SMOKE_TIMEOUT_SECONDS must be a positive integer" >&2
	exit 2
}

for tool in curl jq; do
	command -v "$tool" >/dev/null || {
		echo "missing required command: $tool" >&2
		exit 2
	}
done

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/gregale-execution-smoke.XXXXXX")"
cleanup() {
	local exit_code=$?
	trap - EXIT INT TERM
	rm -rf -- "$tmp_dir"
	exit "$exit_code"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

request() {
	local method="$1"
	local path="$2"
	local payload="${3:-}"
	local body_file="$tmp_dir/body"
	local headers_file="$tmp_dir/headers"
	local -a curl_args=(
		--silent --show-error --connect-timeout 10 --max-time 30
		-X "$method" -D "$headers_file" -o "$body_file" -w '%{http_code}'
		-H "Authorization: Bearer ${FAAS_TOKEN}"
		-H 'Accept: application/json'
	)
	if [[ -n "$payload" ]]; then
		curl_args+=(-H 'Content-Type: application/json' --data "$payload")
	fi
	SMOKE_STATUS="$(curl "${curl_args[@]}" "$GREGALE_API_URL$path")"
	SMOKE_BODY="$(<"$body_file")"
}

fail_with_body() {
	echo "disposable execution release smoke failed: $1" >&2
	if [[ -n "${SMOKE_BODY:-}" ]]; then
		printf '%s\n' "$SMOKE_BODY" | jq -c . >&2 2>/dev/null || printf '%s\n' "$SMOKE_BODY" >&2
	fi
	exit 1
}

request GET /v1/capabilities
[[ "$SMOKE_STATUS" == 200 ]] || fail_with_body "capability endpoint returned HTTP $SMOKE_STATUS"
advertised="$(jq -r '.capabilities[] | select(.key == "disposable-runs") | .enabled' <<<"$SMOKE_BODY")" ||
	fail_with_body 'capability response is not valid JSON'
[[ "$advertised" == true || "$advertised" == false ]] ||
	fail_with_body 'capability response has no boolean disposable-runs row'

probe_payload='{"runtime":"node24","source":"console.log(\"gregale-disposable-smoke-ok\", input.value)","input":{"value":42},"limits":{"timeout_ms":10000,"memory_mb":128,"cpu_millicores":250,"ephemeral_disk_mb":64,"max_output_bytes":65536},"network":{"mode":"none"}}'

if [[ "$advertised" != true ]]; then
	request POST /v1/executions "$probe_payload"
	[[ "$SMOKE_STATUS" == 501 ]] || fail_with_body "capability is disabled but execution admission returned HTTP $SMOKE_STATUS"
	jq -e '.code == "not_implemented"' <<<"$SMOKE_BODY" >/dev/null || fail_with_body 'disabled execution response has the wrong problem code'
	printf 'disposable execution release smoke passed: capability disabled and admission fail-closed\n'
	exit 0
fi

request POST /v1/executions "$probe_payload"
[[ "$SMOKE_STATUS" == 202 ]] || fail_with_body "capability is enabled but execution admission returned HTTP $SMOKE_STATUS"
execution_id="$(jq -er '.id' <<<"$SMOKE_BODY")" || fail_with_body 'execution admission response has no id'

deadline=$((SECONDS + SMOKE_TIMEOUT_SECONDS))
while (( SECONDS < deadline )); do
	request GET "/v1/executions/$execution_id"
	[[ "$SMOKE_STATUS" == 200 ]] || fail_with_body "execution status returned HTTP $SMOKE_STATUS"
	state="$(jq -r '.status // empty' <<<"$SMOKE_BODY")"
	case "$state" in
		succeeded)
		stdout="$(jq -r '.stdout // empty' <<<"$SMOKE_BODY")"
		[[ "$stdout" == *gregale-disposable-smoke-ok* ]] || fail_with_body 'successful execution did not return the smoke stdout marker'
		printf 'disposable execution release smoke passed: capability enabled, execution=%s\n' "$execution_id"
		exit 0
		;;
		failed|cancelled)
		fail_with_body "execution finished with status $state"
		;;
	esac
	sleep 1
done

fail_with_body "execution $execution_id did not reach a terminal state within ${SMOKE_TIMEOUT_SECONDS}s"
