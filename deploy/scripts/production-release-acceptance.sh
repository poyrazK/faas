#!/usr/bin/env bash
set -euo pipefail

: "${RELEASE_SHA:?set RELEASE_SHA to the desired 40-character release SHA}"
: "${RUN_ID:?set RUN_ID to a unique workflow run id}"
: "${ACTIVE_NODE_COUNT:?set ACTIVE_NODE_COUNT from the fleet release report}"

[[ "$RELEASE_SHA" =~ ^[0-9a-f]{40}$ ]] || { echo "invalid RELEASE_SHA" >&2; exit 2; }
[[ "$RUN_ID" =~ ^[0-9]+$ ]] || { echo "invalid RUN_ID" >&2; exit 2; }
[[ "$ACTIVE_NODE_COUNT" =~ ^[1-9][0-9]*$ ]] || { echo "invalid ACTIVE_NODE_COUNT" >&2; exit 2; }
SHARED_RETRY_BUDGET_REQUIRED="${SHARED_RETRY_BUDGET_REQUIRED:-false}"
case "$SHARED_RETRY_BUDGET_REQUIRED" in
	true|false) ;;
	*) echo "SHARED_RETRY_BUDGET_REQUIRED must be true or false" >&2; exit 2 ;;
esac
if [[ "$SHARED_RETRY_BUDGET_REQUIRED" == true ]]; then
	: "${PROMETHEUS_URL:?set PROMETHEUS_URL when shared retry-budget mode is required}"
	EXPECTED_GATEWAY_COUNT="${EXPECTED_GATEWAY_COUNT:-$ACTIVE_NODE_COUNT}"
	[[ "$EXPECTED_GATEWAY_COUNT" =~ ^[1-9][0-9]*$ ]] || {
		echo "EXPECTED_GATEWAY_COUNT must be a positive integer" >&2
		exit 2
	}
fi

GREGALE_BIN="${GREGALE_BIN:-/opt/faas/current/bin/gregale}"
GREGALECTL_BIN="${GREGALECTL_BIN:-/usr/local/bin/gregalectl}"
FAAS_API="${FAAS_API:-https://api.gregale.dev}"
FAAS_APPS_DOMAIN="${FAAS_APPS_DOMAIN:-gregale.dev}"
DEPLOY_TIMEOUT_SECONDS="${DEPLOY_TIMEOUT_SECONDS:-1200}"

[[ -x "$GREGALE_BIN" ]] || { echo "candidate gregale binary is missing: $GREGALE_BIN" >&2; exit 1; }
[[ -x "$GREGALECTL_BIN" ]] || { echo "candidate gregalectl binary is missing: $GREGALECTL_BIN" >&2; exit 1; }
command -v jq >/dev/null
command -v curl >/dev/null

workdir="$(mktemp -d)"
chmod 0700 "$workdir"
credential_file="$workdir/credential.json"
slugs=()
key_id=""

cleanup() {
	set +e
	if [[ -s "$credential_file" ]]; then
		export FAAS_TOKEN
		FAAS_TOKEN="$(jq -r '.token // empty' "$credential_file")"
		for slug in "${slugs[@]:-}"; do
			[[ -n "$slug" ]] && "$GREGALE_BIN" apps -q "$slug" >/dev/null 2>&1
		done
	fi
	if [[ -n "$key_id" ]]; then
		"$GREGALECTL_BIN" release-acceptance revoke-token \
			--key-id "$key_id" --yes --reason=production_release_acceptance >/dev/null 2>&1
	fi
	rm -rf "$workdir"
}
trap cleanup EXIT INT TERM

"$GREGALECTL_BIN" release-acceptance mint-token \
	--yes --reason=production_release_acceptance --ttl=2h >"$credential_file"
chmod 0600 "$credential_file"
key_id="$(jq -er '.key_id' "$credential_file")"
export FAAS_TOKEN
FAAS_TOKEN="$(jq -er '.token' "$credential_file")"
export FAAS_API FAAS_APPS_DOMAIN

short_sha="${RELEASE_SHA:0:8}"
run_suffix="${RUN_ID: -8}"

deploy_one() {
	local template="$1" slug="$2" output="$3"
	FAAS_JSON=1 "$GREGALE_BIN" deploy \
		--template "$template" --name "$slug" --wait --timeout "$DEPLOY_TIMEOUT_SECONDS" \
		--yes --no-require-authn --reason production-release-acceptance >"$output"
}

verify_receipt() {
	local output="$1"
	jq -e '
		(.id | type == "string" and length > 0) and
		.status == "live" and
		.rollout_state == "complete" and
		.hosting_receipt.smoke.status == "verified" and
		.hosting_receipt.smoke.status_code >= 200 and
		.hosting_receipt.smoke.status_code < 300
	' "$output" >/dev/null
	local app_url health_path
	app_url="$(jq -er '.app_url' "$output")"
	health_path="$(jq -r '.hosting_receipt.smoke.path // "/healthz"' "$output")"
	curl --fail --silent --show-error --location \
		--retry 10 --retry-delay 2 --retry-all-errors \
		--connect-timeout 3 --max-time 10 "${app_url}${health_path}" >/dev/null
	# The normal /healthz path may be answered from gateway wake state without
	# entering the guest. Exercise an ordinary origin route as well.
	curl --fail --silent --show-error --location \
		--retry 10 --retry-delay 2 --retry-all-errors \
		--connect-timeout 3 --max-time 10 "${app_url%/}/" >/dev/null
}

assert_policy_bool() {
	local file="$1" field="$2" expected="$3"
	jq -e --arg field "$field" --argjson expected "$expected" '.[$field] == $expected' "$file" >/dev/null || {
		echo "app policy receipt has unexpected $field (wanted $expected)" >&2
		jq -c --arg field "$field" '{slug, ($field): .[$field]}' "$file" >&2
		return 1
	}
}

read_app_policy() {
	local slug="$1" output="$2"
	FAAS_JSON=1 "$GREGALE_BIN" app "$slug" --json >"$output"
}

set_app_policy() {
	local slug="$1" output="$2"
	shift 2
	FAAS_JSON=1 "$GREGALE_BIN" app "$slug" "$@" --json >"$output"
}

wait_for_http_status() {
	local url="$1" expected="$2" headers="$3" body="$4" status=""
	for _ in $(seq 1 15); do
		status="$(curl --silent --show-error --connect-timeout 3 --max-time 10 \
			--dump-header "$headers" --output "$body" --write-out '%{http_code}' "$url" 2>/dev/null || true)"
		if [[ "$status" == "$expected" ]]; then
			return 0
		fi
		sleep 2
	done
	echo "${url} returned HTTP ${status}, wanted ${expected}" >&2
	if [[ -s "$headers" ]]; then
		sed -n '1p' "$headers" >&2
	fi
	if [[ -s "$body" ]]; then
		head -c 1024 "$body" >&2
		echo >&2
	fi
	return 1
}

verify_app_policy_controls() {
	local slug="$1"
	local app_url="https://${slug}.${FAAS_APPS_DOMAIN}"
	local original="$workdir/${slug}-policy-original.json"
	local receipt="$workdir/${slug}-policy-receipt.json"
	local headers="$workdir/${slug}-policy-headers"
	local body="$workdir/${slug}-policy-body"

	read_app_policy "$slug" "$original"
	# Release acceptance deploys a fresh Scale-plan app. Pin its documented
	# defaults so this contract always exercises a real transition and a later
	# default change gets reviewed alongside the CLI behavior.
	jq -e '
		.maintenance_mode == false and
		.streaming_enabled == true and
		.websocket_enabled == true and
		.route_metrics_enabled == true and
		.consumer_auth_mode == "optional"
	' "$original" >/dev/null || {
		echo "fresh acceptance app has unexpected policy defaults" >&2
		jq -c '{slug, maintenance_mode, streaming_enabled, websocket_enabled, route_metrics_enabled, consumer_auth_mode}' "$original" >&2
		return 1
	}

	# The maintenance toggle must reach the public gateway, produce its
	# documented 503 response, and return the app to its original state.
	set_app_policy "$slug" "$receipt" --maintenance
	assert_policy_bool "$receipt" maintenance_mode true
	wait_for_http_status "${app_url%/}/" 503 "$headers" "$body"
	grep -qi '^Retry-After:' "$headers"
	jq -e '.code == "app_maintenance_mode"' "$body" >/dev/null
	set_app_policy "$slug" "$receipt" --no-maintenance
	assert_policy_bool "$receipt" maintenance_mode false
	wait_for_http_status "${app_url%/}/" 200 "$headers" "$body"

	# Consumer auth is an independent gateway gate. Verify anonymous traffic is
	# rejected in required mode, then restore the original optional mode.
	set_app_policy "$slug" "$receipt" --consumer-auth-mode required
	jq -e '.consumer_auth_mode == "required"' "$receipt" >/dev/null
	wait_for_http_status "${app_url%/}/" 401 "$headers" "$body"
	jq -e '.code == "consumer_key_required"' "$body" >/dev/null
	set_app_policy "$slug" "$receipt" --consumer-auth-mode optional
	jq -e '.consumer_auth_mode == "optional"' "$receipt" >/dev/null
	wait_for_http_status "${app_url%/}/" 200 "$headers" "$body"

	# Toggle the three plan-enabled transport/telemetry settings together and
	# read back each value. A disabled WebSocket upgrade has a deterministic
	# 501 response, which proves the gateway consumed the new app policy.
	set_app_policy "$slug" "$receipt" --no-streaming-enabled --no-websocket --no-route-metrics
	assert_policy_bool "$receipt" streaming_enabled false
	assert_policy_bool "$receipt" websocket_enabled false
	assert_policy_bool "$receipt" route_metrics_enabled false
	local ws_status
	ws_status="$(curl --http1.1 --silent --show-error --connect-timeout 3 --max-time 10 \
		--dump-header "$headers" --output "$body" --write-out '%{http_code}' \
		-H 'Connection: Upgrade' -H 'Upgrade: websocket' \
		-H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
		"${app_url%/}/" 2>/dev/null || true)"
	[[ "$ws_status" == 501 ]]
	grep -qi '^x-faas-error-reason: websocket_not_on_plan' "$headers"

	# Restore every changed value and verify the CLI readback matches the saved
	# response before the acceptance app is removed by cleanup.
	set_app_policy "$slug" "$receipt" --streaming-enabled --websocket-enabled --route-metrics
	assert_policy_bool "$receipt" maintenance_mode false
	assert_policy_bool "$receipt" streaming_enabled true
	assert_policy_bool "$receipt" websocket_enabled true
	assert_policy_bool "$receipt" route_metrics_enabled true
	jq -e '.consumer_auth_mode == "optional"' "$receipt" >/dev/null
}

# Submit both execution shapes. Placement is capacity-ranked, not round-robin,
# so this first wave proves both shapes but cannot guarantee node coverage.
pids=()
outputs=()
for i in $(seq 1 "$ACTIVE_NODE_COUNT"); do
	app_slug="ra-${short_sha}-${run_suffix}-a${i}"
	function_slug="ra-${short_sha}-${run_suffix}-f${i}"
	slugs+=("$app_slug" "$function_slug")
	app_output="$workdir/${app_slug}.json"
	function_output="$workdir/${function_slug}.json"
	outputs+=("$app_output" "$function_output")
	deploy_one hello-node "$app_slug" "$app_output" & pids+=("$!")
	deploy_one function-node "$function_slug" "$function_output" & pids+=("$!")
done

failed=0
for pid in "${pids[@]}"; do
	wait "$pid" || failed=1
done
(( failed == 0 )) || { echo "one or more production acceptance deployments failed" >&2; exit 1; }

for output in "${outputs[@]}"; do
	verify_receipt "$output"
done

# The released customer binary owns the complete app-policy mutation path.
# Exercise it against one fresh production app before placement and rollout
# checks, then restore every tested setting before the app is deleted.
verify_app_policy_controls "${slugs[0]}"

slug_csv="$(IFS=,; echo "${slugs[*]}")"
placement_file="$workdir/placement.json"
placement_ok=false
if "$GREGALECTL_BIN" release-acceptance verify-placement --slugs "$slug_csv" >"$placement_file"; then
	placement_ok=true
else
	placement_exit=$?
	(( placement_exit == 3 )) || exit "$placement_exit"
fi

# If the capacity chooser placed the entire first wave on one node, submit
# bounded, serial follow-ups until an actual running/parked instance covers
# every active node. Every added deployment must pass the same receipt and
# public smoke checks; this is not a placement-gate bypass. A saturated or
# unreachable node remains a hard failure after the bounded budget.
max_extra=$((ACTIVE_NODE_COUNT * 4))
coverage_deadline=$((SECONDS + 25 * 60))
for ((i=1; i<=max_extra; i++)); do
	if [[ "$placement_ok" == true ]]; then
		break
	fi
	if ((SECONDS >= coverage_deadline)); then
		break
	fi
	extra_slug="ra-${short_sha}-${run_suffix}-x${i}"
	extra_output="$workdir/${extra_slug}.json"
	slugs+=("$extra_slug")
	deploy_one hello-node "$extra_slug" "$extra_output"
	verify_receipt "$extra_output"
	slug_csv="$(IFS=,; echo "${slugs[*]}")"
	if "$GREGALECTL_BIN" release-acceptance verify-placement --slugs "$slug_csv" >"$placement_file"; then
		placement_ok=true
	else
		placement_exit=$?
		(( placement_exit == 3 )) || exit "$placement_exit"
	fi
done
if [[ "$placement_ok" != true ]]; then
	echo "production acceptance placement did not cover every active node within ${max_extra} extra verified deployments and the 25-minute admission window" >&2
	jq -c . "$placement_file" >&2
	exit 1
fi
jq -e --argjson expected "$ACTIVE_NODE_COUNT" \
	'.ready == true and ((.nodes | length) == $expected)' "$placement_file" >/dev/null

# A redeploy must keep the prior revision publicly serving until the candidate
# finishes post-readiness verification. Probe the hello app's origin / route:
# /healthz defaults to an edge answer and cannot prove workload continuity.
# The final wait uses the shipped CLI, proving it does not return during the
# temporary live state.
redeploy_queued="$workdir/redeploy-queued.json"
FAAS_JSON=1 "$GREGALE_BIN" deploy \
	--template hello-node --name "${slugs[0]}" --no-wait --yes --no-require-authn \
	--reason production-release-redeploy-acceptance >"$redeploy_queued"
redeploy_id="$(jq -er '.id' "$redeploy_queued")"
redeploy_final="$workdir/redeploy-final.json"
FAAS_JSON=1 "$GREGALE_BIN" deployment wait "$redeploy_id" \
	--rollout --timeout "$DEPLOY_TIMEOUT_SECONDS" >"$redeploy_final" &
wait_pid="$!"
probe_headers="$workdir/redeploy-health-headers"
probe_body="$workdir/redeploy-health-body"
while kill -0 "$wait_pid" 2>/dev/null; do
	probe_rc=0
	curl --fail-with-body --silent --show-error --connect-timeout 3 --max-time 10 \
		--dump-header "$probe_headers" --output "$probe_body" \
		"https://${slugs[0]}.${FAAS_APPS_DOMAIN}/" || probe_rc=$?
	if (( probe_rc != 0 )); then
		kill "$wait_pid" 2>/dev/null || true
		wait "$wait_pid" 2>/dev/null || true
		echo "previous serving revision became unavailable during redeploy (curl exit ${probe_rc})" >&2
		if [[ -s "$probe_headers" ]]; then
			# Retain only response metadata. The request-id lets operators
			# correlate this exact failed probe with edge and compute logs.
			sed -n '1p' "$probe_headers" >&2
			grep -i '^x-faas-request-id:' "$probe_headers" >&2 || true
		fi
		if [[ -s "$probe_body" ]]; then
			head -c 2048 "$probe_body" >&2
			echo >&2
		fi
		exit 1
	fi
	sleep 2
done
wait "$wait_pid"
verify_receipt "$redeploy_final"

if [[ "$SHARED_RETRY_BUDGET_REQUIRED" == true ]]; then
	PROMETHEUS_URL="$PROMETHEUS_URL" \
		EXPECTED_GATEWAY_COUNT="$EXPECTED_GATEWAY_COUNT" \
		deploy/scripts/verify-shared-retry-budget.sh
fi

printf 'production acceptance passed: release=%s nodes=%s deployments=%s\n' \
	"$RELEASE_SHA" "$ACTIVE_NODE_COUNT" "${#slugs[@]}"
