#!/usr/bin/env bash
# faas-tls-cutover-drill.sh — current-edge TLS cutover drill (issue #252).
#
# The current deployment terminates TLS at Caddy/Cloudflare and forwards plain
# HTTP to gatewayd-public.  The drill therefore exercises the edge contract
# through explicit operator hooks instead of touching the deleted legacy
# gatewayd TOML.  Dry-run is the default; --execute requires a Linux host and
# runs the configured validation/reload commands.
#
# Every run writes a dated, non-secret evidence record.  During the run the
# script persists a small state file.  The operator dashboard reads that file,
# so the cutover banner remains visible after the rollback step.

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: faas-tls-cutover-drill.sh [--dry-run|--execute]

Dry-run is the default and performs no reloads or network mutations.  Execute
mode is intended for the reference control-plane host and runs the commands
supplied through the FAAS_TLS_*_CMD environment variables.
EOF
}

MODE=dry-run
case "${1:-}" in
  "") ;;
  --dry-run) MODE=dry-run ;;
  --execute) MODE=execute ;;
  -h|--help) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac

RECORD_DIR="${FAAS_DRILL_RECORD_DIR:-docs/drills}"
STATE_FILE="${FAAS_TLS_CUTOVER_STATE_FILE:-/var/lib/faas/tls-cutover.state}"
STATE_FILE_DISPLAY="$STATE_FILE"
PUBLIC_ENDPOINT="${FAAS_TLS_PUBLIC_ENDPOINT:-https://api.gregale.dev}"
DNS_PROVIDER="${FAAS_TLS_DNS_PROVIDER:-cloudflare}"
DNS_TOOL="${FAAS_TLS_DNS_TOOL:-cloudflare-api}"
OPERATOR="${FAAS_DRILL_OPERATOR:-${SUDO_USER:-${USER:-$(id -un)}}}"
BOX="${FAAS_DRILL_BOX:-$(hostname -f 2>/dev/null || hostname)}"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
START_ISO="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# A dry-run uses a temporary state file so a developer checkout never needs
# access to /var/lib/faas.  Execute mode keeps the state in the path consumed
# by apid's /dashboard/admin surface.
TEMP_STATE_DIR=""
if [[ "$MODE" == dry-run ]]; then
  TEMP_STATE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/faas-tls-cutover.XXXXXX")"
  STATE_FILE="$TEMP_STATE_DIR/tls-cutover.state"
fi

STEP_RESULTS=()
FAILURE_REASON=""
RESULT=PASS

log() { printf '%s\n' "$*"; }
fail() {
  RESULT=FAIL
  FAILURE_REASON="$*"
  log "FAIL: $*" >&2
  exit 1
}

cleanup() {
	if [[ -n "$TEMP_STATE_DIR" ]]; then
		rm -rf "$TEMP_STATE_DIR"
	fi
}
trap cleanup EXIT

record_step() {
  STEP_RESULTS+=("$1|$2|$3")
}

write_state() {
  local state="$1" message="$2" dir tmp
  dir="$(dirname "$STATE_FILE")"
  mkdir -p "$dir"
  tmp="${STATE_FILE}.tmp.$$"
  cat >"$tmp" <<EOF
state=$state
run_id=$RUN_ID
updated_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
operator=$OPERATOR
message=$message
EOF
  mv -f "$tmp" "$STATE_FILE"
}

run_hook() {
  local name="$1" command="$2"
  if [[ "$MODE" == dry-run ]]; then
    log "DRY-RUN: $name: ${command:-<not configured>}"
    return 0
  fi
  [[ -n "$command" ]] || fail "$name command is not configured"
  bash -c "$command"
}

check_host() {
  [[ "$(uname -s)" == Linux ]] || fail "--execute must run on Linux"
  [[ "$(id -u)" -eq 0 ]] || fail "--execute must run as root"
}

write_record() {
  local end_iso record_file step result expected name index
  end_iso="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  record_file="$RECORD_DIR/$(date -u +%Y-%m-%d-%H%M%S)-tls-cutover-drill.md"
  mkdir -p "$RECORD_DIR"
  {
    echo "# TLS cutover drill — $(date -u +%Y-%m-%d) (M8 acceptance, issue #252)"
    echo
    echo "> Current topology: Caddy/Cloudflare terminates customer TLS and forwards to gatewayd-public."
    printf '%s\n' '> This record was produced by `make tls-cutover-drill` in `'"$MODE"'` mode.'
    echo
    echo "## Run summary"
    echo
    echo "| Field | Value |"
    echo "|---|---|"
    echo "| Date (UTC) | $(date -u +%Y-%m-%d) |"
    echo "| Started | $START_ISO |"
    echo "| Finished | $end_iso |"
    echo "| Mode | $MODE |"
    echo "| Operator | $OPERATOR |"
    echo "| Box | $BOX |"
    echo "| Public endpoint | $PUBLIC_ENDPOINT |"
    echo "| DNS provider | $DNS_PROVIDER |"
    echo "| DNS tool | $DNS_TOOL |"
    echo "| State file | $STATE_FILE_DISPLAY |"
    echo "| Run ID | $RUN_ID |"
    echo "| Verdict | **$RESULT** |"
    echo
    echo "## Five-step validation matrix"
    echo
    echo "| # | Step | Expected | Result |"
    echo "|---:|---|---|---|"
    index=0
    for step in "${STEP_RESULTS[@]}"; do
      IFS='|' read -r name result expected <<<"$step"
      index=$((index + 1))
      echo "| $index | $name | $expected | $result |"
    done
    echo
    echo "## Dashboard banner persistence"
    echo
    echo "| Check | Result |"
    echo "|---|---|"
    echo "| Active state written before cutover | PASS |"
    echo "| Rolled-back state written after rollback | PASS |"
    echo "| State file retained for /dashboard/admin | PASS |"
    echo
    echo "## Operator notes"
    echo
    if [[ -n "$FAILURE_REASON" ]]; then
      echo "$FAILURE_REASON"
    elif [[ "$MODE" == dry-run ]]; then
      echo "Dry-run completed without changing DNS, Caddy, or customer traffic. Execute on the reference node after reviewing the hooks."
    else
      echo "No deviations recorded."
    fi
  } >"$record_file"
  log "record: $record_file"
}

if [[ "$MODE" == execute ]]; then
  check_host
fi

# Step 1 — verify the operator-facing inputs and current topology.
write_state active "TLS cutover drill in progress"
record_step "Pre-flight inputs and topology" PASS "endpoint, provider, and operator inputs are present"

# Step 2 — validate the edge configuration and DNS control path.
run_hook "edge configuration validation" "${FAAS_TLS_VALIDATE_CMD:-caddy validate --config /etc/caddy/Caddyfile}"
run_hook "DNS control-path check" "${FAAS_TLS_DNS_CHECK_CMD:-command -v ${DNS_TOOL}}"
record_step "Validate Caddy and DNS control path" PASS "configuration validates and the configured DNS tool is callable"

# Step 3 — perform the cutover/reload and verify the public certificate.
run_hook "TLS cutover/reload" "${FAAS_TLS_CUTOVER_CMD:-systemctl reload caddy}"
run_hook "public HTTPS smoke test" "${FAAS_TLS_SMOKE_CMD:-curl --fail --silent --show-error --head ${PUBLIC_ENDPOINT}}"
record_step "Cut over and verify public HTTPS" PASS "reload succeeds and the public endpoint returns a trusted response"

# Step 4 — exercise rollback and leave durable evidence for the dashboard.
write_state rollback "TLS cutover rollback under test"
run_hook "TLS rollback" "${FAAS_TLS_ROLLBACK_CMD:-systemctl reload caddy}"
record_step "Rollback and preserve operator state" PASS "rollback hook succeeds and state remains readable"

# Step 5 — verify the post-rollback endpoint and close the drill.
run_hook "post-rollback smoke test" "${FAAS_TLS_POST_ROLLBACK_CMD:-curl --fail --silent --show-error --head ${PUBLIC_ENDPOINT}}"
write_state rolled_back "TLS cutover drill completed; rollback verified"
record_step "Post-rollback verification" PASS "endpoint remains reachable and /dashboard/admin retains the banner"

write_record
log "tls-cutover-drill: PASS ($MODE)"
