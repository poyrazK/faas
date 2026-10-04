#!/usr/bin/env bash
# Render deploy/ansible/roles/alertmanager/templates/alertmanager.yml.j2 for
# every supported delivery shape, prove amtool accepts each one, and prove
# each severity reaches the receiver the role promises. Nothing else renders
# this template before it reaches a host, so a broken route or receiver used
# to surface only as missing pages in production.
#
# Usage: check_alertmanager_config.sh [repo_root]
# AMTOOL=/path/to/amtool skips the download (local runs); otherwise the
# script fetches the Alertmanager release the role pins and verifies the
# role's SHA-256 before using it.
set -euo pipefail

repo_root=${1:-$(git rev-parse --show-toplevel)}
role="$repo_root/deploy/ansible/roles/alertmanager"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

amtool=${AMTOOL:-}
if [[ -z $amtool ]]; then
  version=$(awk -F'"' '/^am_version:/ {print $2}' "$role/defaults/main.yml")
  sha=$(awk -F'"' '/^am_release_sha256:/ {print $2}' "$role/defaults/main.yml")
  tarball="alertmanager-${version}.linux-amd64.tar.gz"
  curl --fail --silent --show-error --location --retry 3 --retry-delay 2 \
    -o "$work/$tarball" \
    "https://github.com/prometheus/alertmanager/releases/download/v${version}/${tarball}"
  echo "${sha}  $work/$tarball" | sha256sum --check --strict >/dev/null
  tar -xzf "$work/$tarball" -C "$work" "alertmanager-${version}.linux-amd64/amtool"
  amtool="$work/alertmanager-${version}.linux-amd64/amtool"
fi

# Placeholder secret files. Alertmanager reads *_file paths at send time,
# but they must be real paths for a faithful render.
for name in smtp-password pushover-token pushover-user-key discord-webhook-url slack-webhook-url heartbeat-url; do
  printf 'https://example.invalid/%s\n' "$name" >"$work/$name"
done

failed=0

# render <case> <extra vars as JSON>
render() {
  local name=$1 vars=$2
  printf '%s\n' "$vars" >"$work/$name.vars.json"
  ANSIBLE_LOCALHOST_WARNING=false ANSIBLE_INVENTORY_UNPARSED_WARNING=false \
    ANSIBLE_DEPRECATION_WARNINGS=false ANSIBLE_PYTHON_INTERPRETER=auto_silent \
    ansible all -i localhost, -c local \
    -e @"$role/defaults/main.yml" \
    -e @"$work/$name.vars.json" \
    -m ansible.builtin.template \
    -a "src=$role/templates/alertmanager.yml.j2 dest=$work/$name.yml mode=0600" >/dev/null
  if ! "$amtool" check-config "$work/$name.yml" >"$work/$name.check.log" 2>&1; then
    echo "alertmanager config [$name]: amtool check-config failed" >&2
    cat "$work/$name.check.log" >&2
    failed=1
  fi
}

# route <case> <expected receiver> <label=value>...
route() {
  local name=$1 want=$2
  shift 2
  if ! "$amtool" config routes test --config.file="$work/$name.yml" \
    --verify.receivers="$want" "$@" >"$work/route.log" 2>&1; then
    echo "alertmanager config [$name]: {$*} routed to '$(tr -d '\n' <"$work/route.log")', want $want" >&2
    failed=1
  fi
}

render dev '{"am_dev_mode": true}'
route dev faas-silent severity=page component=apid family=x
route dev faas-silent severity=warn component=apid family=x
route dev faas-silent alertname=FaasWatchdog severity=info component=alertmanager family=watchdog

render discord "{\"am_discord_webhook_url_file\": \"$work/discord-webhook-url\", \"am_heartbeat_url_file\": \"$work/heartbeat-url\"}"
route discord faas-page severity=page component=apid family=x
route discord faas-warn severity=warn component=apid family=x
route discord faas-silent severity=info component=apid family=x
route discord faas-heartbeat alertname=FaasWatchdog severity=info component=alertmanager family=watchdog
route discord faas-silent severity=page component=meterd family=alert_preset_signals
grep -q "api_url_file: \"$work/discord-webhook-url\"" "$work/discord.yml" || { echo "alertmanager config [discord]: Discord webhook not rendered as a Slack-compatible receiver" >&2; failed=1; }
grep -q " url_file: \"$work/heartbeat-url\"" "$work/discord.yml" || { echo "alertmanager config [discord]: heartbeat webhook not rendered" >&2; failed=1; }

render slack "{\"am_slack_webhook_url_file\": \"$work/slack-webhook-url\"}"
route slack faas-page severity=page component=apid family=x
route slack faas-warn severity=warn component=apid family=x
route slack faas-silent alertname=FaasWatchdog severity=info component=alertmanager family=watchdog
grep -q 'api_url_file:' "$work/slack.yml" || { echo "alertmanager config [slack]: no slack api_url_file rendered" >&2; failed=1; }

render email_pushover "{\"am_smtp_smarthost\": \"smtp.example.invalid:587\", \"am_smtp_password_file\": \"$work/smtp-password\", \"am_pushover_token_file\": \"$work/pushover-token\", \"am_pushover_user_key_file\": \"$work/pushover-user-key\"}"
route email_pushover faas-page severity=page component=apid family=x
route email_pushover faas-warn severity=warn component=apid family=x

render pushover_only "{\"am_pushover_token_file\": \"$work/pushover-token\", \"am_pushover_user_key_file\": \"$work/pushover-user-key\"}"
route pushover_only faas-page severity=page component=apid family=x
route pushover_only faas-silent severity=warn component=apid family=x

# Secrets are referenced by path only; no placeholder URL may be inlined.
if grep -h 'example.invalid' "$work"/*.yml | grep -v -q 'smtp_smarthost'; then
  echo "alertmanager config: a secret value was inlined into the rendered config" >&2
  failed=1
fi

if [[ $failed -ne 0 ]]; then
  exit 1
fi
echo "alertmanager config: all delivery shapes parse and route as expected"
