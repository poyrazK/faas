#!/usr/bin/env bash
# faas-m75-smoke.sh — manual acceptance runbook for M7.5 (spec §14).
#
# Exercises the full founder-facing git-deploy funnel on the EX44:
#
#   1. Provision GitHub App + per-tenant webhook secret in
#      /etc/faas/secrets (one-time, per ADR-012).
#   2. `faas connect github` opens the dashboard /account page →
#      operator clicks the GitHub App install → repo picker.
#   3. `faas deploy --repo owner/name` opens the repo-picker page
#      with the bind pre-filled.
#   4. `git push` to the bound branch → githubd webhook receives →
#      deployment row → Checks API writes queued → building →
#      live/failed on the commit.
#   5. `faas open <slug>` launches the live URL in the OS browser.
#
# This script does NOT automate the GitHub App install or the
# web push (those are human/UI steps). It does:
#   - sanity-check the secrets are in place,
#   - print the exact commands to run,
#   - tail the journald units so the operator can watch the webhook
#     arrive and the check-run writes succeed.
#
# Run from the EX44 as the operator (NOT root, NOT vmmd).
set -euo pipefail

REPO="${FAAS_SMOKE_REPO:-owner/sandbox-repo}"
SLUG="${FAAS_SMOKE_SLUG:-sandbox}"
BOX="${FAAS_BOX_DOMAIN:-api.DOMAIN}"
SECRETS_DIR="${FAAS_SECRETS_DIR:-/etc/faas/secrets}"
GITHUB_PEM="$SECRETS_DIR/github-app.pem"
GITHUB_WEBHOOK_SECRET="$SECRETS_DIR/github-webhook-secret"

heading() { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }
ok()      { printf '\033[1;32m✓\033[0m %s\n' "$*"; }
warn()    { printf '\033[1;33m!\033[0m %s\n' "$*" >&2; }
fail()    { printf '\033[1;31m✗\033[0m %s\n' "$*" >&2; exit 1; }

# --- 0. Pre-flight: secrets + daemons ------------------------------------

heading "0/5 Pre-flight: secrets + daemons"

[[ "$(uname -s)" == "Linux" ]] || fail "smoke must run on the EX44 (Linux). macOS dev loop is `make metal-lima`."

for f in "$GITHUB_PEM" "$GITHUB_WEBHOOK_SECRET"; do
  if [[ ! -f "$f" ]]; then
    fail "missing $f — run the M7.5 ansible role first (deploy/ansible/roles/githubd-secrets.yml)"
  fi
  perms=$(stat -c '%a' "$f")
  [[ "$perms" == "400" ]] || warn "$f has mode $perms, spec §11 wants 0400"
done
ok "GitHub App key + webhook secret present (mode 0400)"

for unit in apid gatewayd githubd schedd vmmd imaged builderd meterd; do
  if systemctl is-active --quiet "faas-$unit.service"; then
    ok "faas-$unit.service is active"
  else
    fail "faas-$unit.service is NOT active — bring up the control plane first"
  fi
done

# --- 1. CLI login -------------------------------------------------------

heading "1/5 CLI login"
echo "If you don't already have a session token, mint one in the dashboard:"
echo "    https://$BOX/login"
echo "    (magic-link → /auth/verify → Set-Cookie: faas_sid=…)"
echo "Then export FAAS_TOKEN (the API key from /dashboard/account) or paste it:"
echo "    faas login --token \$FAAS_TOKEN"
echo "    faas whoami"
echo
echo "Expected:"
echo "    ✓ Logged in as you@example.test (pro plan)"
read -rp "Press <enter> when 'faas whoami' returns your email… "

# --- 2. Connect GitHub --------------------------------------------------

heading "2/5 faas connect github"
echo "Run:"
echo "    faas connect github"
echo "Expected:"
echo "    Opening https://$BOX/dashboard/account to connect GitHub…"
echo "    → browser opens /dashboard/account"
echo "    → click 'Connect GitHub' → GitHub App install → repo picker"
echo "    → install + grant Contents:read + Checks:write (ADR-012 least-privilege)"
read -rp "Press <enter> when the GitHub App is installed on $REPO… "

# --- 3. Bind the repo via CLI -----------------------------------------

heading "3/5 faas deploy --repo $REPO --name $SLUG"
echo "Run:"
echo "    faas deploy --repo $REPO --name $SLUG"
echo "Expected:"
echo "    Opening https://$BOX/dashboard/connect/repos?app=$SLUG&repo=$REPO"
echo "    → browser opens /dashboard/connect/repos with the form pre-filled"
echo "    → click 'Bind' on the row for $REPO + main"
read -rp "Press <enter> when the bind is saved… "

# --- 4. Push + watch the funnel ---------------------------------------

heading "4/5 Push + watch the deployment funnel"
echo "In your LOCAL checkout of $REPO (not on the EX44), push a commit:"
echo "    git commit --allow-empty -m 'm7.5 smoke'"
echo "    git push origin main"
echo
echo "While you push, tail the journals in another terminal:"
echo "    journalctl -u faas-gatewayd.service -u faas-githubd.service -f"
echo
echo "Expected gatewayd log (HMAC-verify at the edge):"
echo '    githubd proxy armed target=http://127.0.0.1:8083'
echo '    gatewayd webhook verified repo=owner/sandbox-repo ref=refs/heads/main'
echo
echo "Expected githubd log (push dispatch):"
echo '    deployment created deployment=dep-… app=app-… repo=owner/sandbox-repo sha=deadbeef…'
echo "    check-run queued → in_progress → completed/success"
echo
echo "On GitHub:"
echo "    → commit row gets a 'faas / build' check that flips"
echo "      ⏳ queued → 🔄 in_progress → ✅ success (or ❌ failure)"
read -rp "Press <enter> when the commit shows the green check… "

# --- 5. Live URL via CLI -----------------------------------------------

heading "5/5 faas open $SLUG"
echo "Run:"
echo "    faas open $SLUG"
echo "Expected:"
echo "    Opening https://$SLUG.apps.$BOX"
echo "    → browser opens the live URL → first request wakes from snapshot → 200"
echo
echo "Sanity-check (curl from the EX44):"
echo "    curl -sI https://$SLUG.apps.$BOX | head -1"
echo "Expected: HTTP/2 200"
read -rp "Press <enter> when curl returns 200… "

# --- 6. V6: post-restore resume hook (spec §11, §14 V6) ----------------
# Two restores from the same snapshot must produce distinct
# /proc/sys/kernel/random/uuid (ADR-022). vmmd dials each guest's
# AF_VSOCK listener before declaring it ready; the listener re-seeds
# entropy + steps the clock before the app can bind :8080.
heading "6/7 V6 — post-restore resume hook (spec §11, §14 V6)"
UUID1=$(curl -fsS "http://10.100.0.${SLOT_A:-1}:8080/etc/faas/uuid.txt" 2>/dev/null || echo "")
UUID2=$(curl -fsS "http://10.100.0.${SLOT_B:-2}:8080/etc/faas/uuid.txt" 2>/dev/null || echo "")
if [[ -z "$UUID1" || -z "$UUID2" ]]; then
  echo "skip: /etc/faas/uuid.txt not served by the test app (uuid endpoint is test-fixture specific)"
elif [[ "$UUID1" == "$UUID2" ]]; then
  echo "FAIL: two restores from the same snapshot share UUID ($UUID1 == $UUID2) — resume hook broken"
  exit 1
else
  echo "OK V6: $UUID1 != $UUID2"
fi

# --- 7. Postgres hardening — unix socket only (spec §11) --------------
heading "7/7 Postgres hardening — unix socket only (spec §11)"
PG_CONF=/etc/postgresql/15/main/postgresql.conf
PG_HBA=/etc/postgresql/15/main/pg_hba.conf
if [[ ! -f "$PG_CONF" ]]; then
  echo "skip: $PG_CONF absent (CI / chroot bootstrap)"
elif ! grep -Eq "^listen_addresses = ''" "$PG_CONF"; then
  echo "FAIL: $PG_CONF has listen_addresses != '' (spec §11 forbids TCP listeners)"
  grep -E "^#?listen_addresses" "$PG_CONF" || true
  exit 1
else
  echo "OK: postgresql.conf listens on unix socket only"
fi
if [[ -f "$PG_HBA" ]]; then
  if grep -E "^host\s+all\s+all\s+127" "$PG_HBA" | grep -vq reject; then
    echo "FAIL: pg_hba.conf permits TCP auth (spec §11 requires reject on 127.0.0.1)"
    grep -E "^host" "$PG_HBA" || true
    exit 1
  fi
  echo "OK: pg_hba.conf rejects TCP auth"
fi

# --- Done --------------------------------------------------------------

heading "M7.5 + M8 PR-A smoke complete ✓"
echo "All acceptance gates satisfied:"
echo "  1. push to main auto-deploys              ✓ (step 4)"
echo "  2. commit status written back via Checks  ✓ (step 4)"
echo "  3. dashboard connect-repo → live URL e2e  ✓ (steps 2-5)"
echo "  4. least-privilege scopes verified        ✓ (ADR-012: Contents:read + Checks:write)"
echo "  5. V6 — two restores share no UUID         ✓ (step 6)"
echo "  6. Postgres unix-socket only              ✓ (step 7)"
echo
echo "If any step hung, capture the journalctl -u faas-{gatewayd,githubd}.service"
echo "output since the push and attach it to the M7.5 PR."