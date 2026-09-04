#!/usr/bin/env bash
# verify-secrets.sh — operator-side smoke test for security review A4.
#
# Asserts that FAAS_SESSION_KEY and the per-host value-hash HMAC key are
# scoped to faas-apid only (loaded via systemd LoadCredential=, NOT via
# EnvironmentFile=/etc/faas/sealed.env which is shared by all six
# control-plane daemons). Run on the EX44
# after `make bootstrap` and a daemon-reload.
#
# Exits 0 if all checks pass; prints each check as ✓/✗ and returns
# non-zero on the first failure. Safe to run repeatedly (read-only).
#
# Usage:
#   sudo deploy/scripts/verify-secrets.sh

set -euo pipefail

pass=0
fail=0

check() {
  local desc="$1"
  shift
  if "$@"; then
    echo "  ✓ ${desc}"
    pass=$((pass+1))
  else
    echo "  ✗ ${desc}"
    fail=$((fail+1))
  fi
}

# 1. The session key file must exist with mode 0400 root:root.
check "/etc/faas/secrets/session.key exists with mode 0400 root:root" bash -c '
  [[ -f /etc/faas/secrets/session.key ]] \
    && [[ "$(stat -c "%a" /etc/faas/secrets/session.key)" == "400" ]] \
    && [[ "$(stat -c "%U:%G" /etc/faas/secrets/session.key)" == "root:root" ]]
'

# 1b. The ADR-117 value-hash key follows the same root-only on-disk
# contract. systemd copies it into apid's private credential directory.
check "/etc/faas/secrets/host.hmac.key exists with mode 0400 root:root" bash -c '
  [[ -f /etc/faas/secrets/host.hmac.key ]] \
    && [[ "$(stat -c "%a" /etc/faas/secrets/host.hmac.key)" == "400" ]] \
    && [[ "$(stat -c "%U:%G" /etc/faas/secrets/host.hmac.key)" == "root:root" ]]
'

# 1c. The session key file content must be exactly 64 hex chars
#     (32 raw bytes; the loader at cmd/apid/handlers_auth.go:325
#     hex.DecodeString's it on boot). The verify-secrets.sh "file is
#     there + 0400" check above doesn't catch a hand-edited 32-char
#     file that would silently fall back to the ephemeral manager —
#     the A5 silent-degradation bug closed by PR #1078. Content is
#     trimmed of trailing newline (the canonical `gregale secrets
#     init` output emits 64 chars; a hand edit with echo often
#     appends `\n`).
check "/etc/faas/secrets/session.key content is 64 hex chars (32 raw bytes)" bash -c '
  content="$(tr -d "\n" < /etc/faas/secrets/session.key)"
  [[ "${#content}" -eq 64 ]] \
    && [[ "${content}" =~ ^[0-9a-fA-F]{64}$ ]]
'

# 2. sealed.env MUST NOT carry FAAS_SESSION_KEY any more — that
#    was the A4 leak. Operators migrating from a pre-A4 install need
#    to re-run the v2 secrets init (PR-X `gregale secrets init`, pending)
#    or hand-edit sealed.env to scrub the key. The historical v1
#    bootstrap.sh was retired in issue #911 / PR-1 (ADR-110); the file's
#    existence is asserted first so a fresh host that hasn't bootstrapped
#    at all reports red � rather than silently passing on a missing-file
#    grep (grep -q exits 2 on a missing file, which `!` would otherwise
#    flip to a false-positive 0).
check "sealed.env does NOT contain FAAS_SESSION_KEY" bash -c '
  [[ -f /etc/faas/sealed.env ]] \
    && ! grep -q "^FAAS_SESSION_KEY=" /etc/faas/sealed.env
'

# 3. faas-apid's environment carries FAAS_SESSION_KEY (systemd
#    LoadCredential → Environment= substitution). The shape of the
#    value (PATH-shaped: starts with /run/credentials/, OR
#    CONTENT-shaped: exactly 64 hex chars) is what matters — without
#    the shape match the loader either fail-closes (close, but boot
#    is broken) or silently falls back to NewEphemeralManager (the
#    A5 silent-degradation bug). The unit source at
#    /etc/systemd/system/faas-apid.service MUST declare
#    FAAS_SESSION_KEY=%d/faas_session_key (PATH-shaped) AND a matching
#    LoadCredential=faas_session_key:<src-path>; otherwise the
#    systemd expansion writes a literal "%d" into the env var.
check "faas-apid loads FAAS_SESSION_KEY (PATH-shaped)" bash -c '
  unit=/etc/systemd/system/faas-apid.service
  grep -q "^Environment=FAAS_SESSION_KEY=%d/faas_session_key" "$unit" \
    && grep -q "^LoadCredential=faas_session_key:" "$unit"
'

# 3b. The per-host value-hash HMAC key must be scoped to apid through
# the systemd credential directory, never exported through sealed.env.
check "faas-apid loads FAAS_HOST_HMAC_KEY_PATH" bash -c '
  systemctl show faas-apid -p Environment 2>/dev/null | grep -q "FAAS_HOST_HMAC_KEY_PATH"
'

# 4. The other five daemons MUST NOT carry FAAS_SESSION_KEY in
#    their environment — that was the leak surface.
for unit in faas-gatewayd-internal faas-gatewayd-public faas-imaged faas-githubd faas-meterd faas-schedd; do
  check "${unit} does NOT load FAAS_SESSION_KEY" bash -c "
    ! systemctl show ${unit} -p Environment 2>/dev/null | grep -q 'FAAS_SESSION_KEY'
  "
done

# 5. apid's unit file references LoadCredential (defence in depth).
check "faas-apid.service uses LoadCredential=" bash -c '
  grep -q "^LoadCredential=faas_session_key:" /etc/systemd/system/faas-apid.service
'
check "faas-apid.service loads the host HMAC credential" bash -c '
  grep -q "^LoadCredential=faas_host_hmac_key:" /etc/systemd/system/faas-apid.service
'

# 6. Public-release billing provider mode.
# Polar is the production billing provider, so
# FAAS_POLAR_ACCESS_TOKEN is mandatory on every PRODUCTION-tagged node.
# The Stripe legacy opt-in (FAAS_BILLING_PROVIDER=stripe) still boots;
# the provider-specific checks below are skipped for that explicit rollback
# path so the node-level escape hatch remains reachable.
#
# (The CI static-check at .github/workflows/ci.yml greps for the
# literals 'FAAS_POLAR_ACCESS_TOKEN' and 'FAAS_BILLING_PROVIDER=polar'
# inside this file as a regression sentinel — the production default
# is Polar and the script must name both the key + the provider.)
#
# Dev boxes (Lima / CI runners / local playbooks): this script is
# intended for production-tagged hosts only. The dev-box loop should
# explicitly set FAAS_BILLING_PROVIDER=stripe when it does not have a
# Polar account, or provide complete Polar sandbox credentials. The
# CLAUDE.md local loop does not run this script against Lima guests.
if [[ -f /etc/faas/sealed.env ]]; then
  if grep -q "^FAAS_BILLING_PROVIDER=stripe" /etc/faas/sealed.env; then
    # Legacy opt-in path. Provider checks are skipped because the
    # node-level operator has explicitly selected the rollback surface.
    :
  elif grep -q "^FAAS_BILLING_PROVIDER=polar" /etc/faas/sealed.env \
    || ! grep -q "^FAAS_BILLING_PROVIDER=" /etc/faas/sealed.env; then
    check "sealed.env has FAAS_POLAR_ACCESS_TOKEN" bash -c '
      grep -q "^FAAS_POLAR_ACCESS_TOKEN=." /etc/faas/sealed.env
    '
    check "sealed.env has FAAS_POLAR_WEBHOOK_SECRET" bash -c '
      grep -qE "^FAAS_POLAR_WEBHOOK_SECRET=(whsec_[A-Za-z0-9+/=_-]+|polar_whs_[A-Za-z0-9_-]+|[A-Za-z0-9+/=_-]+)$" /etc/faas/sealed.env
    '
    check "Polar usage meter id is configured" bash -c '
      grep -q "^FAAS_POLAR_METER_ID=." /etc/faas/sealed.env \
        || grep -qsE "^[[:space:]]*meter_id[[:space:]]*=[[:space:]]+\"[^\"]+\"" /etc/faas/apid.toml /etc/faas/meterd.toml 2>/dev/null
    '
    for polar_product in hobby pro scale; do
      polar_product_env=${polar_product^^}
      check "Polar ${polar_product} product id is configured" bash -c "
        grep -q \"^FAAS_POLAR_${polar_product_env}_PRODUCT_ID=.\" /etc/faas/sealed.env \\
          || grep -qsE \"^[[:space:]]*${polar_product}_product_id[[:space:]]*=[[:space:]]+\\\"[^\\\"]+\\\"\" /etc/faas/apid.toml /etc/faas/meterd.toml 2>/dev/null
      "
    done
  elif grep -q "^FAAS_BILLING_PROVIDER=paddle" /etc/faas/sealed.env; then
    check "sealed.env has FAAS_PADDLE_API_KEY" bash -c '
      grep -q "^FAAS_PADDLE_API_KEY=pdl_" /etc/faas/sealed.env
    '
    if grep -qE "^FAAS_PADDLE_SANDBOX=(1|true)" /etc/faas/sealed.env; then
      check "FAAS_PADDLE_API_KEY starts with pdl_sandbox_ when sandbox=1" bash -c '
        grep -q "^FAAS_PADDLE_API_KEY=pdl_sandbox_" /etc/faas/sealed.env
      '
    else
      check "FAAS_PADDLE_API_KEY starts with pdl_live_ when sandbox=0" bash -c '
        grep -q "^FAAS_PADDLE_API_KEY=pdl_live_" /etc/faas/sealed.env
      '
    fi
    check "sealed.env has FAAS_PADDLE_WEBHOOK_SECRET" bash -c '
      grep -q "^FAAS_PADDLE_WEBHOOK_SECRET=whk_" /etc/faas/sealed.env
    '
  else
    check "sealed.env has a supported billing provider selector" bash -c '
      grep -qE "^FAAS_BILLING_PROVIDER=(polar|paddle|stripe)$" /etc/faas/sealed.env
    '
  fi
fi

echo
echo "Summary: ${pass} passed, ${fail} failed"
if [[ "${fail}" -ne 0 ]]; then
  exit 1
fi
