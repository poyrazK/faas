#!/usr/bin/env bash
# Hermetic tests for alertmanager_email_vars.sh, and the CD contract that runs
# it in both control-plane converges.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$repo_root/scripts/ci/alertmanager_email_vars.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
run() { # VARS_FILE [VAR=VALUE...]
  local vars=$1
  shift
  env -i PATH="$PATH" "$@" bash "$script" "$vars"
}
operator() {
  printf '%s\n' 'faas_box_role: control-plane' \
    'am_slack_webhook_url_file: /etc/alertmanager/secrets/slack-webhook-url' \
    'am_heartbeat_url_file: /etc/alertmanager/secrets/heartbeat-url' \
    'am_email_to_page: old@example.com' > "$1"
}

# Unset: nothing changes.
operator "$work/none.yml"
cp "$work/none.yml" "$work/none.want"
run "$work/none.yml" || fail "unset failed"
cmp -s "$work/none.yml" "$work/none.want" || fail "unset changed the vars"

# Set: email replaces the operator's email keys, keeps the rest, and the
# chat webhook stays unless ALERTMANAGER_CHAT_WEBHOOK=off.
operator "$work/email.yml"
run "$work/email.yml" ALERTMANAGER_EMAIL_TO=ops@example.com || fail "email failed"
grep -qx 'faas_box_role: control-plane' "$work/email.yml" || fail "dropped an unrelated variable"
grep -qx 'am_heartbeat_url_file: /etc/alertmanager/secrets/heartbeat-url' "$work/email.yml" || fail "dropped the heartbeat"
grep -qx 'am_slack_webhook_url_file: /etc/alertmanager/secrets/slack-webhook-url' "$work/email.yml" || fail "dropped chat without off"
[[ "$(grep -c '^am_email_to_page:' "$work/email.yml")" == 1 ]] || fail "duplicate am_email_to_page"
grep -qx 'am_email_to_page: "ops@example.com"' "$work/email.yml" || fail "no page address"
grep -qx 'am_email_to_warn: "ops@example.com"' "$work/email.yml" || fail "no warn address"
grep -qx 'am_smtp_smarthost: "smtp.resend.com:587"' "$work/email.yml" || fail "no default relay"
grep -qx 'am_smtp_password_file: /etc/alertmanager/secrets/smtp-password' "$work/email.yml" || fail "no password file"
grep -qx 'am_smtp_require_tls: true' "$work/email.yml" || fail "TLS not required"

# off: every chat webhook file is cleared, once.
operator "$work/off.yml"
run "$work/off.yml" ALERTMANAGER_EMAIL_TO=ops@example.com ALERTMANAGER_CHAT_WEBHOOK=off || fail "off failed"
[[ "$(grep -c '^am_slack_webhook_url_file:' "$work/off.yml")" == 1 ]] || fail "duplicate slack key"
grep -qx 'am_slack_webhook_url_file: ""' "$work/off.yml" || fail "slack webhook kept"
grep -qx 'am_discord_webhook_url_file: ""' "$work/off.yml" || fail "discord webhook kept"

# Bad values are refused and leave the file alone.
for bad in 'ALERTMANAGER_EMAIL_TO=not-an-email' 'ALERTMANAGER_EMAIL_TO=a@b.co ALERTMANAGER_SMTP_SMARTHOST=relay' \
  'ALERTMANAGER_EMAIL_TO=a@b.co ALERTMANAGER_EMAIL_FROM=x"y@z.co'; do
  operator "$work/bad.yml"
  cp "$work/bad.yml" "$work/bad.want"
  # shellcheck disable=SC2086
  if run "$work/bad.yml" $bad 2>/dev/null; then fail "accepted $bad"; fi
  cmp -s "$work/bad.yml" "$work/bad.want" || fail "$bad changed the vars"
done

# The rendered file is still one YAML mapping Ansible can load (where PyYAML
# is installed, as on the fleet runner).
if python3 -c 'import yaml' 2>/dev/null; then
  python3 -c 'import sys,yaml; d=yaml.safe_load(open(sys.argv[1])); assert d["am_smtp_require_tls"] is True and d["am_email_from"]=="Gregale Alerts <noreply@gregale.dev>"' "$work/off.yml" \
    || fail "not loadable YAML"
fi

# Both control-plane converges run it.
count=$(grep -c 'bash scripts/ci/alertmanager_email_vars.sh "\$work/vars.yml"' "$repo_root/.github/workflows/cd-controlplane.yml")
[[ "$count" == 2 ]] || fail "cd-controlplane.yml runs the script $count times, want 2"
echo "alertmanager_email_vars: ok"
