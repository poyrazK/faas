#!/usr/bin/env bash
# alertmanager_email_vars.sh VARS_FILE
#
# Routes the control plane's alerts by email through an SMTP relay when the
# deploy environment sets ALERTMANAGER_EMAIL_TO (a GitHub environment
# variable, not a secret). The alertmanager role renders email_configs for
# faas-page and faas-warn from these variables; the relay password stays a
# root-provisioned host file (am_smtp_password_file, alertmanager 0400) and
# never passes through CD. ALERTMANAGER_CHAT_WEBHOOK=off also drops the chat
# webhook files the environment's operator variables name, so a placeholder
# webhook cannot fail every notification (2026-11-21: the owner chose email
# after the hc-ping placeholder made Alertmanager report every send as
# failed). Without ALERTMANAGER_EMAIL_TO the file is left untouched.
set -euo pipefail

[[ $# -eq 1 ]] || {
  echo "usage: alertmanager_email_vars.sh VARS_FILE" >&2
  exit 2
}
vars=$1
to=${ALERTMANAGER_EMAIL_TO:-}
[[ -n "$to" ]] || exit 0

valid_email='^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$'
[[ "$to" =~ $valid_email ]] || {
  echo "::error::ALERTMANAGER_EMAIL_TO is not one email address" >&2
  exit 1
}
smarthost=${ALERTMANAGER_SMTP_SMARTHOST:-smtp.resend.com:587}
[[ "$smarthost" =~ ^[A-Za-z0-9.-]+:[0-9]+$ ]] || {
  echo "::error::ALERTMANAGER_SMTP_SMARTHOST must be host:port" >&2
  exit 1
}
username=${ALERTMANAGER_SMTP_USERNAME:-resend}
[[ "$username" =~ ^[A-Za-z0-9._@+-]+$ ]] || {
  echo "::error::ALERTMANAGER_SMTP_USERNAME has unexpected characters" >&2
  exit 1
}
from=${ALERTMANAGER_EMAIL_FROM:-Gregale Alerts <noreply@gregale.dev>}
[[ "$from" =~ ^[A-Za-z0-9\ ._-]*\<?[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\>?$ ]] || {
  echo "::error::ALERTMANAGER_EMAIL_FROM is not an address or 'Name <address>'" >&2
  exit 1
}

keys='am_smtp_smarthost|am_smtp_password_file|am_smtp_auth_username|am_smtp_require_tls|am_email_from|am_email_to_page|am_email_to_warn'
if [[ "${ALERTMANAGER_CHAT_WEBHOOK:-}" == off ]]; then
  keys+='|am_slack_webhook_url_file|am_discord_webhook_url_file|am_slack_warn_webhook_url_file|am_discord_warn_webhook_url_file'
fi
touch "$vars"
tmp=$(mktemp "${vars}.XXXXXX")
grep -vE "^(${keys}):" "$vars" > "$tmp" || true
{
  printf 'am_smtp_smarthost: "%s"\n' "$smarthost"
  printf 'am_smtp_password_file: /etc/alertmanager/secrets/smtp-password\n'
  printf 'am_smtp_auth_username: "%s"\n' "$username"
  printf 'am_smtp_require_tls: true\n'
  printf 'am_email_from: "%s"\n' "$from"
  printf 'am_email_to_page: "%s"\n' "$to"
  printf 'am_email_to_warn: "%s"\n' "$to"
  if [[ "${ALERTMANAGER_CHAT_WEBHOOK:-}" == off ]]; then
    printf 'am_slack_webhook_url_file: ""\nam_discord_webhook_url_file: ""\n'
    printf 'am_slack_warn_webhook_url_file: ""\nam_discord_warn_webhook_url_file: ""\n'
  fi
} >> "$tmp"
chmod --reference="$vars" "$tmp" 2>/dev/null || chmod 0600 "$tmp"
mv -f "$tmp" "$vars"
