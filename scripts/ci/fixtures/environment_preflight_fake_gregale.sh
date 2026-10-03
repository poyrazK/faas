#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$@" >"$ACTION_TEST_ARGS"
printf '%s' "${FAAS_TOKEN:-}" >"$ACTION_TEST_TOKEN"
printf '%s' "${FAAS_API:-}" >"$ACTION_TEST_API"
if [[ "${ACTION_TEST_STATUS:-ready}" == "blocked" ]]; then
	printf '{"status":"blocked","qualification":{"id":"q-123"},"promotion_preview":{"can_promote":false,"approval_required":true},"blocking_reasons":["release is not promotable"]}\n'
	exit 1
fi
printf '{"status":"ready","qualification":{"id":"q-123"},"promotion_preview":{"can_promote":true,"approval_required":true},"blocking_reasons":[]}\n'
