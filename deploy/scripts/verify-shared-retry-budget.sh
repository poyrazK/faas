#!/usr/bin/env bash
set -euo pipefail

: "${PROMETHEUS_URL:?set PROMETHEUS_URL to the Prometheus API base URL}"
: "${EXPECTED_GATEWAY_COUNT:?set EXPECTED_GATEWAY_COUNT to the serving gateway count}"

[[ "$EXPECTED_GATEWAY_COUNT" =~ ^[1-9][0-9]*$ ]] || {
	echo "EXPECTED_GATEWAY_COUNT must be a positive integer" >&2
	exit 2
}
[[ "$PROMETHEUS_URL" =~ ^https?://[^[:space:]]+$ ]] || {
	echo "PROMETHEUS_URL must be an http:// or https:// base URL" >&2
	exit 2
}
command -v curl >/dev/null
command -v jq >/dev/null

prometheus_url="${PROMETHEUS_URL%/}"

query_value() {
	local query="$1"
	local response
	response="$(curl --fail --silent --show-error \
		--connect-timeout 3 --max-time 10 --get \
		--data-urlencode "query=$query" \
		"$prometheus_url/api/v1/query")"
	jq -er '
		if .status != "success" then error("Prometheus query failed")
		elif (.data.result | length) != 1 then error("Prometheus query did not return one value")
		else (.data.result[0].value[1] | tonumber | tostring)
		end
	' <<<"$response"
}

assert_equal() {
	local label="$1" got="$2" want="$3"
	if ! awk -v got="$got" -v want="$want" 'BEGIN { exit (got + 0 == want + 0) ? 0 : 1 }'; then
		echo "$label=$got, want $want" >&2
		exit 1
	fi
}

active_gateways="$(query_value 'count(up{job="gatewayd-internal"} == 1) or vector(0)')"
shared_gateways="$(query_value 'count(gateway_retry_budget_shared{job="gatewayd-internal"} == 1) or vector(0)')"
backend_series="$(query_value 'count(gateway_retry_budget_backend_info{job="gatewayd-internal"}) or vector(0)')"
backend_ids="$(query_value 'count(count by (backend_id) (gateway_retry_budget_backend_info{job="gatewayd-internal"})) or vector(0)')"
backend_errors="$(query_value 'sum(increase(gateway_retry_budget_backend_operations_total{job="gatewayd-internal",result="error"}[10m])) or vector(0)')"

assert_equal "active gateway targets" "$active_gateways" "$EXPECTED_GATEWAY_COUNT"
assert_equal "shared-mode gateways" "$shared_gateways" "$EXPECTED_GATEWAY_COUNT"
assert_equal "reported backend identities" "$backend_series" "$EXPECTED_GATEWAY_COUNT"
assert_equal "unique shared backends" "$backend_ids" 1
if ! awk -v errors="$backend_errors" 'BEGIN { exit (errors + 0 == 0) ? 0 : 1 }'; then
	echo "retry-budget Redis errors in the last 10m=$backend_errors, want 0" >&2
	exit 1
fi

printf 'shared retry-budget gate passed: gateways=%s backend_ids=%s redis_errors_10m=%s\n' \
	"$active_gateways" "$backend_ids" "$backend_errors"
