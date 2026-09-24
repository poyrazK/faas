#!/usr/bin/env bash
set -euo pipefail

: "${RELEASE_SHA:?set RELEASE_SHA to the desired 40-character release SHA}"
: "${RUN_ID:?set RUN_ID to a unique workflow run id}"
: "${ACTIVE_NODE_COUNT:?set ACTIVE_NODE_COUNT from the fleet release report}"

[[ "$RELEASE_SHA" =~ ^[0-9a-f]{40}$ ]] || { echo "invalid RELEASE_SHA" >&2; exit 2; }
[[ "$RUN_ID" =~ ^[0-9]+$ ]] || { echo "invalid RUN_ID" >&2; exit 2; }
[[ "$ACTIVE_NODE_COUNT" =~ ^[1-9][0-9]*$ ]] || { echo "invalid ACTIVE_NODE_COUNT" >&2; exit 2; }

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

# A redeploy must keep the prior revision publicly healthy until the candidate
# finishes post-readiness verification. The final wait uses the shipped CLI,
# proving it does not return during the temporary live state.
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
		"https://${slugs[0]}.${FAAS_APPS_DOMAIN}/healthz" || probe_rc=$?
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

printf 'production acceptance passed: release=%s nodes=%s deployments=%s\n' \
	"$RELEASE_SHA" "$ACTIVE_NODE_COUNT" "${#slugs[@]}"
