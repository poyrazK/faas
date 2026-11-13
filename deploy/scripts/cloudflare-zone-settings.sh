#!/usr/bin/env bash
# Hunt 34: owner-run, free-zone settings; see CloudflareZoneTraffic.md.
# plan (default): read settings. bots: allow AI crawlers, disable Bot Fight
# Mode/JSD and Bot Preference Sync (the owner's 2026-11-13 decision).
# grpc: enable the zone toggle, ONLY for a separately verified direct-origin path.
# No DNS, Tunnel, ruleset, subscription or paid-product writes. APPLY=1 writes.
# The owner types the token at a TTY. It never enters argv, env or a file.
set +x
set -euo pipefail

die() { echo "$*" >&2; exit 1; }
action=${1:-plan}
case "$action" in plan|bots|grpc) ;; *) die 'usage: cloudflare-zone-settings.sh [plan|bots|grpc]' ;; esac
[[ $# -le 1 ]] || die 'unexpected arguments'
case "${APPLY:-0}" in 0|1) ;; *) die 'APPLY must be 0 or 1' ;; esac
zone=c0baeb8d33b48ec1704766edcb695394
[[ ${CF_ZONE_ID:-$zone} == "$zone" ]] || die 'this script is reviewed for gregale.dev only'
[[ -z ${CF_API_TOKEN:-} ]] || die 'unset CF_API_TOKEN; type the token at the TTY prompt'
command -v jq >/dev/null || die 'jq is required'
command -v curl >/dev/null || die 'curl is required'
exec 3<>/dev/tty || die 'run this script in the owner terminal with a TTY'
unset token # Do not inherit an exported variable with the same name.
printf 'Cloudflare token (Zone Read, Zone Settings Read/Edit, Bot Management Read/Edit): ' >&3
IFS= read -r -s token <&3 || die 'could not read token'
printf '\n' >&3
[[ $token =~ ^[A-Za-z0-9_-]+$ && ${#token} -le 256 ]] || die 'invalid token format'
trap 'unset token' EXIT
api=https://api.cloudflare.com/client/v4/zones/$zone
cf() {
  local method=$1 path=$2 response
  shift 2
  # -q ignores ~/.curlrc; @- reads the secret header from a pipe, not argv.
  # No redirect following, retries or environment proxies with a credential.
  response=$(printf 'Authorization: Bearer %s\n' "$token" |
    curl -q --silent --show-error --fail --proto '=https' --noproxy '*' \
      --connect-timeout 10 --max-time 30 --header @- \
      --header 'Content-Type: application/json' --request "$method" "$api$path" "$@") ||
    die "Cloudflare $method $path failed; no automatic retry"
  jq -e '.success == true and (.result | type == "object")' >/dev/null <<<"$response" ||
    die "Cloudflare $method $path returned an unsuccessful or malformed response"
  jq -c '.result' <<<"$response"
}
identity=$(cf GET '')
jq -e --arg zone "$zone" '.id == $zone and .name == "gregale.dev" and .status == "active" and .paused == false and .plan.legacy_id == "free"' >/dev/null <<<"$identity" ||
  die 'expected the active, unpaused gregale.dev Free zone; nothing written'
bots=$(cf GET /bot_management)
grpc=$(cf GET /settings/grpc)
jq -e '(.fight_mode | type == "boolean") and (.ai_bots_protection == null or .ai_bots_protection == "block" or .ai_bots_protection == "disabled" or .ai_bots_protection == "only_on_ad_pages") and ((has("enable_js") | not) or (.enable_js | type == "boolean"))' >/dev/null <<<"$bots" ||
  die 'missing or unknown bot settings; nothing written'
jq -e '.id == "grpc" and (.value == "on" or .value == "off")' >/dev/null <<<"$grpc" ||
  die 'missing or unknown gRPC setting; nothing written'
echo 'zone: gregale.dev (Free); scope: entire zone, including apex and api'
echo "bot settings: $(jq -c '{fight_mode, enable_js, ai_bots_protection, ai_training, ai_search, ai_user, crawler_protection, is_robots_txt_managed, bot_preference_sync_enabled, content_bots_protection}' <<<"$bots")"
echo "gRPC: $(jq -c '{value, editable}' <<<"$grpc")"
[[ $action != plan ]] || exit 0
if [[ $action == bots ]]; then
  desired='{"fight_mode":false,"enable_js":false,"ai_bots_protection":"disabled","ai_search":"disabled","ai_training":"disabled","ai_user":"disabled","bot_preference_sync_enabled":false}'
  path=/bot_management method=PUT current=$bots
  # "disabled" means Allow (do not block) for the AI access-policy fields.
  # Free responses can omit enable_js; traffic verification remains required.
  # Bot Fight Mode off alone did not stop JSD in the browser-applied capture.
  bots_match() {
    jq -e --argjson desired "$desired" '. as $current |
      all($desired | to_entries[]; . as $field |
        if $field.key == "enable_js" then $current[$field.key] != true
        elif $field.key == "ai_bots_protection" then ($current[$field.key] == null or $current[$field.key] == $field.value)
        else $current[$field.key] == $field.value end)' >/dev/null <<<"$1"
  }
  bots_match "$bots" && {
    echo 'ok: bot settings already match; verify customer traffic'; exit 0;
  }
else
  echo 'The gRPC toggle cannot repair public Tunnel hostnames (unsupported by Cloudflare).'
  [[ $(jq -r .editable <<<"$grpc") == true ]] || die 'gRPC is not editable; nothing written'
  desired='{"value":"on"}'
  path=/settings/grpc method=PATCH current=$grpc
  [[ $(jq -r .value <<<"$grpc") != on ]] || { echo 'ok: gRPC already on'; exit 0; }
fi
echo "would: $method $path $desired"
echo "rollback values: $(jq -c --argjson desired "$desired" 'with_entries(select(.key as $k | $desired | has($k)))' <<<"$current")"
[[ ${APPLY:-0} == 1 ]] || { echo 'dry run: set APPLY=1 to write'; exit 0; }
cf "$method" "$path" --data "$desired" >/dev/null
after=$(cf GET "$path")
if [[ $action == bots ]]; then
  bots_match "$after" ||
    die 'bot readback differs; settings may be partially applied; inspect plan and verify HTML'
else
  jq -e '.id == "grpc" and .value == "on"' >/dev/null <<<"$after" || die 'gRPC readback differs; inspect plan'
fi
echo 'done: API readback matches; traffic verification is still required'
