#!/usr/bin/env bash
# bootstrap.sh — one-shot setup for the FaaS control plane on a DigitalOcean
# Droplet (Ubuntu 24.04). Installs Postgres 15, creates system users, drops
# systemd units + TOML configs, runs DB migrations, and starts services.
#
# Usage:
#   curl -sSf https://raw.githubusercontent.com/poyrazK/faas/main/deploy/digitalocean/bootstrap.sh | sudo bash -s
# or:
#   sudo bash deploy/digitalocean/bootstrap.sh
#
# The script auto-detects the Droplet's public IPv4. Override:
#   sudo DROPLET_IP=1.2.3.4 bash deploy/digitalocean/bootstrap.sh

set -euo pipefail

# ─── Constants ────────────────────────────────────────────────────────────────
FAAS_ROOT="/opt/faas"
FAAS_BIN="${FAAS_ROOT}/bin"
FAAS_SRC="${FAAS_ROOT}/src"
CONFIG_DIR="/etc/faas"
SECRETS_DIR="${CONFIG_DIR}/secrets"
SEALED_ENV="${CONFIG_DIR}/sealed.env"
RUN_DIR="/run/faas"
LOG_DIR="/var/log/faas"
SPOOL_DIR="/var/spool/faas"
SNAP_DIR="/srv/fc/snap"
DEPLOY_KEY_PATH="${FAAS_ROOT}/.ssh/deploy_ed25519"

DAEMONS=(apid schedd gatewayd imaged meterd githubd)
SERVICE_USERS=(faas-apid faas-schedd faas-imaged faas-meterd)

# ─── Helpers ──────────────────────────────────────────────────────────────────
step() { echo -e "\n\033[1;36m▸ $1\033[0m"; }
ok()   { echo -e "  \033[1;32m✓ $1\033[0m"; }
warn() { echo -e "  \033[1;33m⚠ $1\033[0m"; }
die()  { echo -e "  \033[1;31m✗ $1\033[0m" >&2; exit 1; }

# ─── Detect IP ────────────────────────────────────────────────────────────────
if [[ -z "${DROPLET_IP:-}" ]]; then
  # DigitalOcean metadata API
  DROPLET_IP=$(curl -sf http://169.254.169.254/metadata/v1/interfaces/public/0/ipv4/address 2>/dev/null || true)
  if [[ -z "${DROPLET_IP}" ]]; then
    DROPLET_IP=$(curl -sf https://ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}')
  fi
fi
step "Droplet IP: ${DROPLET_IP}"
APPS_DOMAIN="${DROPLET_IP}.nip.io"
ok "Apps domain: ${APPS_DOMAIN}"

# ─── 1. System packages ──────────────────────────────────────────────────────
step "Installing system packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq postgresql postgresql-contrib libpq-dev \
  git curl build-essential e2fsprogs jq ufw > /dev/null
ok "Packages installed"

# ─── 2. Go toolchain ─────────────────────────────────────────────────────────
# SHA-256 pinned (issue #193): bumping GO_VERSION requires bumping
# GO_SHA256_LINUX_AMD64 at the same time, in the same commit. Fetch the new
# value from https://go.dev/dl/?mode=json&include=all (filter os=linux,
# arch=amd64, kind=archive) and paste it verbatim. The script refuses to
# install on a mismatch — same fail-loud contract as the EX44 ansible binary
# pin pattern (deploy/ansible/roles/firecracker/tasks/main.yml) and the CI
# vacuum pin (.github/workflows/ci.yml). A backdoored Go on a fresh box
# compiles every production daemon; the pin is the only thing standing
# between a go.dev CDN compromise and root.
step "Installing Go toolchain"
GO_VERSION="1.25.7"
GO_SHA256_LINUX_AMD64="12e6d6a191091ae27dc31f6efc630e3a3b8ba409baf3573d955b196fdf086005"
if ! command -v go &>/dev/null || [[ "$(go version)" != *"go${GO_VERSION}"* ]]; then
  GO_TARBALL="/tmp/go${GO_VERSION}.linux-amd64.tar.gz"
  echo "→ downloading https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
  curl --fail --silent --show-error --location --output "$GO_TARBALL" \
    "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
  echo "→ verifying SHA-256"
  echo "${GO_SHA256_LINUX_AMD64}  ${GO_TARBALL}" | sha256sum --check --strict
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "$GO_TARBALL"
  rm -f "$GO_TARBALL"
  ln -sf /usr/local/go/bin/go /usr/local/bin/go
  ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
fi
ok "Go $(go version | awk '{print $3}')"

# ─── 3. System users & group ─────────────────────────────────────────────────
step "Creating system users"
getent group faas >/dev/null || groupadd --system faas
for u in "${SERVICE_USERS[@]}"; do
  id "$u" &>/dev/null || useradd --system --no-create-home --shell /usr/sbin/nologin --gid faas "$u"
  ok "User $u"
done
# faas user (for gatewayd + githubd)
id faas &>/dev/null || useradd --system --no-create-home --shell /usr/sbin/nologin --gid faas faas
ok "User faas"

# ─── 4. Directories ──────────────────────────────────────────────────────────
step "Creating directories"
BASE_DIR="/srv/fc/base"
LIB_DIR="/var/lib/faas/githubd"
mkdir -p "${FAAS_BIN}" "${FAAS_SRC}" "${CONFIG_DIR}" "${SECRETS_DIR}" \
  "${RUN_DIR}" "${LOG_DIR}" "${SPOOL_DIR}" "${SNAP_DIR}" "${BASE_DIR}" "${LIB_DIR}"
chown root:faas "${CONFIG_DIR}" "${SECRETS_DIR}"
chmod 0750 "${CONFIG_DIR}" "${SECRETS_DIR}"
chown faas-apid:faas "${LOG_DIR}" "${SPOOL_DIR}"
chmod 0750 "${LOG_DIR}" "${SPOOL_DIR}"
chown faas-imaged:faas "${SNAP_DIR}" "${BASE_DIR}"
chmod 0750 "${SNAP_DIR}" "${BASE_DIR}"
chown -R faas:faas "${RUN_DIR}" /var/lib/faas
chmod 0770 "${RUN_DIR}" /var/lib/faas "${LIB_DIR}"
ok "Directories created"

# ─── 5. Postgres setup ───────────────────────────────────────────────────────
step "Configuring PostgreSQL"
systemctl enable --now postgresql

# Create faas role + database if they don't exist.
su - postgres -c "psql -tAc \"SELECT 1 FROM pg_roles WHERE rolname='faas'\"" | grep -q 1 \
  || su - postgres -c "createuser faas"
su - postgres -c "psql -tAc \"SELECT 1 FROM pg_database WHERE datname='faas'\"" | grep -q 1 \
  || su - postgres -c "createdb -O faas faas"

# Enable citext extension (required by migrations).
su - postgres -c "psql -d faas -c 'CREATE EXTENSION IF NOT EXISTS citext;'"

# Ensure peer auth works for the service users → faas DB.
PG_HBA=$(su - postgres -c "psql -tAc 'SHOW hba_file'")
if ! grep -q 'faas_map' "${PG_HBA}"; then
  sed -i '/local   all             all                                     peer/i # FaaS service users\nlocal   faas   all      peer  map=faas_map\n' "${PG_HBA}"
  ok "pg_hba.conf updated"
fi

# Add ident map so service users map to the 'faas' pg role.
PG_IDENT=$(su - postgres -c "psql -tAc 'SHOW ident_file'")
if ! grep -q 'faas_map' "${PG_IDENT}"; then
  cat >> "${PG_IDENT}" <<'EOF'
# Map system users to the faas Postgres role.
faas_map  faas-apid    faas
faas_map  faas-schedd  faas
faas_map  faas-imaged  faas
faas_map  faas-meterd  faas
faas_map  faas         faas
EOF
  ok "pg_ident.conf updated"
fi

systemctl reload postgresql
ok "PostgreSQL configured"

# ─── 6. Firewall (UFW) ───────────────────────────────────────────────────────
step "Configuring UFW firewall (spec §11 — only gatewayd :8080 + SSH are public)"
# Default-deny incoming, default-allow outgoing. Loopback is unaffected, so the
# CD smoke test (curl 127.0.0.1:8080/healthz) keeps working.
#
# Configure the firewall BEFORE we start any service so there's no window
# where e.g. apid is bound 0.0.0.0:8081 without a firewall in front of it
# (issue #85, PR 2).
#
# Idempotency: we do NOT `ufw --force reset` on a re-bootstrap — that would
# silently drop any operator-added rules (e.g. 443/tcp added for TLS).
# `ufw allow` is itself idempotent; defaults are only applied on a fresh
# install (UFW inactive).
if ! ufw status 2>/dev/null | grep -q "Status: active"; then
  ufw --force reset > /dev/null
  ufw default deny incoming > /dev/null
  ufw default allow outgoing > /dev/null
  ok "UFW defaults: deny incoming, allow outgoing"
fi
ufw allow 22/tcp > /dev/null       # SSH (DigitalOcean console fallback)
ufw allow 8080/tcp > /dev/null     # gatewayd — the only public listener
ufw --force enable > /dev/null
ok "UFW active: 22/tcp + 8080/tcp allowed"
# NOTE: TLS production deployments additionally need 443/tcp + 80/tcp (ACME
# http-01); the README "Production / TLS" section walks the operator through
# adding those after pointing a real domain at the droplet.

# ─── 7. Clone / update source ────────────────────────────────────────────────
step "Fetching source code"
if [[ -d "${FAAS_SRC}/.git" ]]; then
  git -C "${FAAS_SRC}" pull --ff-only
elif [[ ! -d "${FAAS_SRC}" || -z "$(ls -A "${FAAS_SRC}")" ]]; then
  git clone https://github.com/poyrazK/faas.git "${FAAS_SRC}"
else
  warn "Directory ${FAAS_SRC} is not empty and not a git repo — skipping clone"
fi
ok "Source at ${FAAS_SRC}"

# ─── 8. Build binaries ───────────────────────────────────────────────────────
step "Building daemons"
# Stop services first to avoid text file busy on overwrite
for svc in apid schedd gatewayd imaged meterd githubd; do
  systemctl stop "faas-${svc}.service" 2>/dev/null || true
done

cd "${FAAS_SRC}"
make build
mkdir -p "${FAAS_BIN}"
find bin -maxdepth 1 -type f -exec install -m 0755 {} "${FAAS_BIN}/" \;
# Also build the migrate tool
go build -o bin/migrate ./cmd/migrate
install -m 0755 bin/migrate "${FAAS_BIN}/"
ok "Binaries in ${FAAS_BIN}"

# ─── 9. Drop configs ─────────────────────────────────────────────────────────
step "Installing configs"
CP_CONFIG_SRC="${FAAS_SRC}/deploy/controlplane"

# TOML configs — sed-replace __DROPLET_IP__
for f in "${CP_CONFIG_SRC}/config/"*.toml; do
  base=$(basename "$f")
  sed "s/__DROPLET_IP__/${DROPLET_IP}/g" "$f" > "${CONFIG_DIR}/${base}"
  chown root:faas "${CONFIG_DIR}/${base}"
  chmod 0640 "${CONFIG_DIR}/${base}"
  ok "${base}"
done

# sealed.env
# Dev API token — 24 random bytes → 48 hex chars, prefixed with `fp_live_` to
# match the fp_live_<48-hex> format enforced by api.ValidAPIKeyFormat
# (pkg/api/apikey.go:17-19, apiKeyRandomBytes=24). Generated per-bootstrap so
# no committed secret ever leaves the repo (issue #85, PR 2).
DEV_TOKEN="fp_live_$(openssl rand -hex 24)"
# Security review A4: FAAS_SESSION_KEY is no longer written to
# sealed.env. The 32-byte hex key is written to
# /etc/faas/secrets/session.key with mode 0400 root:root, and apid
# reads it via systemd LoadCredential= + Environment= (see
# deploy/digitalocean/systemd/faas-apid.service). The other five
# control-plane daemons load sealed.env but don't read the key, so
# scoping it to apid-only closes the leak without code changes.
SESS_KEY_PATH="${SECRETS_DIR}/session.key"
mkdir -p "${SECRETS_DIR}"
openssl rand -hex 32 > "${SESS_KEY_PATH}"
chown root:root "${SESS_KEY_PATH}"
chmod 0400 "${SESS_KEY_PATH}"
# Fail-loud precondition: refuse to continue if the perms drifted.
SESS_KEY_MODE=$(stat -c '%a' "${SESS_KEY_PATH}")
SESS_KEY_OWNER=$(stat -c '%U:%G' "${SESS_KEY_PATH}")
if [[ "${SESS_KEY_MODE}" != "400" || "${SESS_KEY_OWNER}" != "root:root" ]]; then
  die "session key perms drifted: mode=${SESS_KEY_MODE} owner=${SESS_KEY_OWNER} (want 400 root:root)"
fi
ok "session key written to ${SESS_KEY_PATH} (0400 root:root)"
sed -e "s/__DROPLET_IP__/${DROPLET_IP}/g" \
    -e "s|__DEV_TOKEN__|${DEV_TOKEN}|g" \
    "${CP_CONFIG_SRC}/sealed.env.example" > "${SEALED_ENV}"
chown root:faas "${SEALED_ENV}"
chmod 0640 "${SEALED_ENV}"
ok "sealed.env created (dev token generated; session key is in /etc/faas/secrets/session.key)"

# Operator-facing credentials file. Mode 0600 / root:root so the dev token
# never ends up in stdout (which systemd-journal captures), in agent logs,
# or in a shared terminal scrollback (issue #85, PR 2). apid reads the same
# value from /etc/faas/sealed.env on boot.
#
# Re-bootstrap overwrites this file with the new token (rotation). The
# prior token's hash remains valid in PG until FAAS_DEV_TOKEN in sealed.env
# is rotated back, so a careless re-bootstrap does not invalidate a still-
# issued token. The timestamp line lets the operator tell rotations apart
# when reading the file later.
CRED_FILE="/root/faas-dev-credentials.txt"
{
  echo "# FaaS dev credentials — mode 0600, root:root. Do NOT commit."
  echo "# This file is overwritten on every bootstrap (token rotation)."
  echo "# Authoritative value lives in /etc/faas/sealed.env."
  echo "# To rotate without re-bootstrapping: see README 'Post-merge operator actions'."
  echo "FAAS_DEV_TOKEN=${DEV_TOKEN}  # generated $(date -u +%Y-%m-%dT%H:%M:%SZ)"
} > "${CRED_FILE}"
chmod 0600 "${CRED_FILE}"
ok "Dev credentials written to ${CRED_FILE}"

# ─── 10. Systemd units ───────────────────────────────────────────────────────
step "Installing systemd units"
for f in "${CP_CONFIG_SRC}/systemd/"*.{service,slice}; do
  [[ -f "$f" ]] || continue
  cp "$f" /etc/systemd/system/
  ok "$(basename "$f")"
done
systemctl daemon-reload
ok "systemd reloaded"

# ─── 11. Run migrations ──────────────────────────────────────────────────────
step "Running database migrations"
su - faas -s /bin/bash -c "DATABASE_URL='postgres:///faas?host=/run/postgresql&user=faas' ${FAAS_BIN}/migrate"
ok "Migrations applied"

# ─── 11b. Host age key (IAM-2 / issue #186) ─────────────────────────────────
# vmmd normally writes host.age on first boot with mode 0400 root:root
# (spec §11), but vmmd is NOT deployed on DigitalOcean (no /dev/kvm) —
# bootstrap.sh has to generate the key here so MFA handlers (/verify,
# /confirm, /recover, /disable) have an identity to unseal TOTP
# secrets with. `hostage-gen` is built by `make build` next to apid
# (cmd/hostage-gen) and calls secretbox.GenerateAndSaveHostKey +
# WriteRecipientFile internally so the on-disk shape is byte-identical
# to vmmd's first-boot path. The file is 0400 root:root — only the
# owner (root, via vmmd) can read it on disk; apid consumes the
# identity through systemd's LoadCredential=faas_host_age_identity
# (deploy/systemd/faas-apid.service), which copies the file into
# the apid unit's credential dir owned by faas-apid:faas. The on-disk
# mode and the apid-read path are independent — that decoupling is
# what lets us hold the 0400 contract even though apid needs to
# unseal MFA envelopes.
#
# Without this step the MFA handlers 503 CodeCapacity on every step-
# up, locking mfa_pending customers out of every account-scoped route.
HOST_AGE_PATH="${SECRETS_DIR}/host.age"
HOST_AGE_PUB="${SECRETS_DIR}/host.age.pub"
if [[ -x "${FAAS_BIN}/hostage-gen" ]]; then
  if [[ ! -f "${HOST_AGE_PATH}" ]]; then
    "${FAAS_BIN}/hostage-gen" "${HOST_AGE_PATH}" "${HOST_AGE_PUB}"
    chown root:root "${HOST_AGE_PATH}"
    chown root:faas "${HOST_AGE_PUB}"
    chmod 0400 "${HOST_AGE_PATH}"
    chmod 0444 "${HOST_AGE_PUB}"
    ok "host.age written to ${HOST_AGE_PATH} (0400 root:root)"
    ok "host.age.pub written to ${HOST_AGE_PUB} (0444 root:faas)"
  else
    # Drift check: re-bootstrap must not silently rotate the key
    # (a rotation invalidates every customer's MFA enrollment).
    # We refuse; the operator runs `sudo rm /etc/faas/secrets/host.age`
    # explicitly if they mean to.
    HA_MODE=$(stat -c '%a' "${HOST_AGE_PATH}")
    HA_OWNER=$(stat -c '%U:%G' "${HOST_AGE_PATH}")
    if [[ "${HA_MODE}" != "400" || "${HA_OWNER}" != "root:root" ]]; then
      chown root:root "${HOST_AGE_PATH}"
      chmod 0400 "${HOST_AGE_PATH}"
      ok "host.age perms repaired (was ${HA_MODE} ${HA_OWNER}, now 0400 root:root)"
    else
      ok "host.age already present with correct perms (refusing to rotate)"
    fi
  fi
else
  warn "hostage-gen not found at ${FAAS_BIN}/hostage-gen — MFA won't function until you build it (`make build`)"
fi

# ─── 11b-overlap. Rotation overlap detection (issue #316 / ADR-057) ─────────
# Informational only: bootstrap.sh does NOT refuse on .previous
# existence (the runbook explicitly tolerates the overlap window).
# The operator sees "rotation in progress, N days old" alongside
# the host.age status line and can decide whether to wait or to
# invoke `gregale host-age prune-previous --force` to shorten the
# overlap. Without this block the only way to know a rotation is
# in flight is to read host.age.previous by hand — which is the
# kind of latent signal that gets lost between shifts.
HOST_AGE_PREV="${SECRETS_DIR}/host.age.previous"
if [[ -f "${HOST_AGE_PREV}" ]]; then
  HAP_MODE=$(stat -c '%a' "${HOST_AGE_PREV}" 2>/dev/null || echo "??")
  HAP_MTIME_EPOCH=$(stat -c '%Y' "${HOST_AGE_PREV}" 2>/dev/null || echo "0")
  HAP_AGE_DAYS=$(awk -v now="$(date +%s)" -v mtime="${HAP_MTIME_EPOCH}" 'BEGIN { if (mtime > 0) { printf "%d", int((now - mtime) / 86400) } else { print "?" } }')
  HAP_AGE_HOURS=$(awk -v now="$(date +%s)" -v mtime="${HAP_MTIME_EPOCH}" 'BEGIN { if (mtime > 0) { printf "%d", int(((now - mtime) % 86400) / 3600) } else { print "?" } }')
  ok "host.age.previous present — rotation overlap in progress (age ${HAP_AGE_DAYS}d ${HAP_AGE_HOURS}h, mode ${HAP_MODE})"
  if [[ "${HAP_MODE}" != "400" ]]; then
    chown root:root "${HOST_AGE_PREV}"
    chmod 0400 "${HOST_AGE_PREV}"
    warn "host.age.previous perms repaired (was ${HAP_MODE}, now 0400 root:root)"
  fi
fi

# ─── 11c. Cosign sign-keypair (Tier 3 / ADR-038 §Phase 3) ────────────────────
# imaged and schedd fail-loud at startup if /etc/faas/secrets/sign.key
# or sign-pub.pem is missing or has wrong perms (cmd/imaged/main.go:103-109
# and cmd/schedd/main.go:248-251). The cosign signer/verifier is the
# build-attestation pipeline (ADR-038); without these files the daemons
# crash-loop indefinitely. The Go side enforces the file MODE only
# (0440 root:faas for the priv side via pkg/cosign.WriteKeyPairForGroup;
# 0444 root:root for the pub side). The installer (bootstrap.sh here,
# the ansible role in deploy/ansible/) is responsible for the OWNERSHIP
# because the install context varies — root in bootstrap, the faas
# user in ansible, and only the install caller knows the target user.
# Drift-repair branch mirrors 11b's host.age flow: refuse silent
# rotation; only repair perms.
SIGN_KEY_PATH="${SECRETS_DIR}/sign.key"
SIGN_PUB_PATH="${SECRETS_DIR}/sign-pub.pem"
if [[ ! -f "${SIGN_KEY_PATH}" || ! -f "${SIGN_PUB_PATH}" ]]; then
  if [[ -x "${FAAS_BIN}/faas" ]]; then
    "${FAAS_BIN}/faas" sign-keys init \
      --sign-key "${SIGN_KEY_PATH}" --verify-key "${SIGN_PUB_PATH}"
    chown root:faas "${SIGN_KEY_PATH}"
    chmod 0440 "${SIGN_KEY_PATH}"
    chown root:root "${SIGN_PUB_PATH}"
    chmod 0444 "${SIGN_PUB_PATH}"
    ok "sign.key written to ${SIGN_KEY_PATH} (0440 root:faas)"
    ok "sign-pub.pem written to ${SIGN_PUB_PATH} (0444 root:root)"
  else
    warn "faas CLI not found at ${FAAS_BIN}/faas — imaged and schedd will fail-loud until you build it (`make build`) and run \`faas sign-keys init\`"
  fi
else
  # Drift check: re-bootstrap must not silently rotate the key (a
  # rotation invalidates every customer's build-verification chain
  # until schedd reloads the new pub). We refuse rotation; we DO
  # repair perms — same shape as step 11b's host.age drift branch.
  SK_MODE=$(stat -c '%a' "${SIGN_KEY_PATH}")
  SK_OWNER=$(stat -c '%U:%G' "${SIGN_KEY_PATH}")
  SP_MODE=$(stat -c '%a' "${SIGN_PUB_PATH}")
  SP_OWNER=$(stat -c '%U:%G' "${SIGN_PUB_PATH}")
  if [[ "${SK_MODE}" != "440" || "${SK_OWNER}" != "root:faas" \
     || "${SP_MODE}" != "444" || "${SP_OWNER}" != "root:root" ]]; then
    chown root:faas "${SIGN_KEY_PATH}"; chmod 0440 "${SIGN_KEY_PATH}"
    chown root:root "${SIGN_PUB_PATH}"; chmod 0444 "${SIGN_PUB_PATH}"
    ok "sign-keypair perms repaired (was ${SK_MODE} ${SK_OWNER} / ${SP_MODE} ${SP_OWNER}, now 0440 root:faas / 0444 root:root)"
  else
    ok "sign-keypair already present with correct perms (refusing to rotate)"
  fi
fi

# ─── 11d. Hetzner Storage Box secrets (issue #250 / off-host pg backup) ────
# The Hetzner Storage Box holds nightly pg_basebackup copies + the
# continuous WAL archive. rclone authenticates to its SFTP endpoint
# via a sealed-at-rest config under /etc/faas/secrets/storage-box/:
#
#   * rclone.conf  — the `[hertznerbox]` remote (SFTP user, host, key
#     file path). 0400 root:root. Read by `rclone --config=...` in the
#     postgres + faas-pg-basebackup-push units via systemd LoadCredential=.
#   * box-age-key  — the local age identity used to seal the rclone.conf
#     on the operator laptop before shipping to the box. 0400 root:root.
#
# Sealing flow (operator laptop):
#   $ age -r host.age.pub -o rclone.conf.age rclone.conf
#   $ age-keygen -o box-age.key         # 1.1.0+ age identity
#   $ scp rclone.conf.age host:$(hostname).rclone.conf.age
#   $ scp box-age.key      host:$(hostname).box-age.key
#
# The bootstrap step below (a) creates the 0700 root:root staging dir
# if missing, (b) decrypts the .age envelopes to plaintext under it
# with mode 0400 root:root, and (c) drift-repairs perms without ever
# re-rotating (rotation invalidates the rclone session + would let
# a future bootstrap silently strand every nightly push).
#
# This mirrors the 11b (host.age) and 11c (cosign sign-keypair)
# precedent: refuse silent rotation, only repair perms.
SB_DIR="${SECRETS_DIR}/storage-box"
RCLONE_CONF_SRC="/root/rclone.conf.age"
RCLONE_CONF="${SB_DIR}/rclone.conf"
BOX_AGE_SRC="/root/box-age.key"
BOX_AGE="${SB_DIR}/box-age-key"

mkdir -p "${SB_DIR}"
chown root:root "${SB_DIR}"
chmod 0700 "${SB_DIR}"

# rclone.conf
# Mode is 0440 root:postgres — the postgres user (User= on the
# systemd postgresql.service) must read this file via its
# `archive_command` (issue #250). LoadCredential= surfaces the
# plaintext under $CREDENTIALS_DIRECTORY at unit activation; the
# postgres user traverses the dir via the unit's group membership.
# This mirrors the cosign sign-keypair precedent (sign.key is
# 0440 root:faas so faas-imaged can read it via group access).
if [[ ! -f "${RCLONE_CONF}" ]]; then
  if [[ -f "${RCLONE_CONF_SRC}" && -x "${FAAS_BIN}/gregale" ]]; then
    "${FAAS_BIN}/gregale" backup unseal-rclone \
      --age-identity "${BOX_AGE}" \
      --in "${RCLONE_CONF_SRC}" \
      --out "${RCLONE_CONF}"
    chown root:postgres "${RCLONE_CONF}"
    chmod 0440 "${RCLONE_CONF}"
    ok "rclone.conf written to ${RCLONE_CONF} (0440 root:postgres)"
    # Shred the .age envelope on disk so it can't be replayed against a
    # future host.age-key compromise. The plaintext under /etc/faas/
    # secrets/storage-box/ is what units actually consume.
    shred -u "${RCLONE_CONF_SRC}"
    ok "shredded staging ${RCLONE_CONF_SRC}"
  elif [[ ! -f "${BOX_AGE}" ]]; then
    warn "no rclone.conf at ${RCLONE_CONF} and no age envelope at ${RCLONE_CONF_SRC}"
    warn "off-host pg backup will be DISABLED until you scp the .age envelope and re-run"
    warn "(see docs/runbooks/PostgresBackup.md §Preconditions — issue #250)"
  else
    warn "rclone.conf missing at ${RCLONE_CONF}; skip unseal (no ${RCLONE_CONF_SRC})"
  fi
else
  RC_MODE=$(stat -c '%a' "${RCLONE_CONF}")
  RC_OWNER=$(stat -c '%U:%G' "${RCLONE_CONF}")
  if [[ "${RC_MODE}" != "440" || "${RC_OWNER}" != "root:postgres" ]]; then
    chown root:postgres "${RCLONE_CONF}"
    chmod 0440 "${RCLONE_CONF}"
    ok "rclone.conf perms repaired (was ${RC_MODE} ${RC_OWNER}, now 0440 root:postgres)"
  else
    ok "rclone.conf already present with correct perms (refusing to rotate)"
  fi
fi

# box-age-key (the local age identity). Same drift-repair branch.
if [[ ! -f "${BOX_AGE}" ]]; then
  if [[ -f "${BOX_AGE_SRC}" ]]; then
    install -m 0400 -o root -g root "${BOX_AGE_SRC}" "${BOX_AGE}"
    shred -u "${BOX_AGE_SRC}"
    ok "box-age-key written to ${BOX_AGE} (0400 root:root)"
  else
    warn "no box-age-key at ${BOX_AGE} and no plaintext at ${BOX_AGE_SRC}"
    warn "off-host pg backup will be DISABLED until the operator provides it"
  fi
else
  BA_MODE=$(stat -c '%a' "${BOX_AGE}")
  BA_OWNER=$(stat -c '%U:%G' "${BOX_AGE}")
  if [[ "${BA_MODE}" != "400" || "${BA_OWNER}" != "root:root" ]]; then
    chown root:root "${BOX_AGE}"
    chmod 0400 "${BOX_AGE}"
    ok "box-age-key perms repaired (was ${BA_MODE} ${BA_OWNER}, now 0400 root:root)"
  else
    ok "box-age-key already present with correct perms (refusing to rotate)"
  fi
fi

# ─── 12. Generate deploy SSH key ─────────────────────────────────────────────
mkdir -p "$(dirname "${DEPLOY_KEY_PATH}")"
# Defensive ownership on the directory regardless of which branch runs
# (issue #85, PR 2 — review nit #6).
chown -R root:root "$(dirname "${DEPLOY_KEY_PATH}")"
chmod 0700 "$(dirname "${DEPLOY_KEY_PATH}")"
if [[ ! -f "${DEPLOY_KEY_PATH}" ]]; then
  ssh-keygen -t ed25519 -N '' -C 'faas-cd-deploy' -f "${DEPLOY_KEY_PATH}"
  # Add to authorized_keys for root
  mkdir -p /root/.ssh && chmod 700 /root/.ssh
  cat "${DEPLOY_KEY_PATH}.pub" >> /root/.ssh/authorized_keys
  chmod 600 /root/.ssh/authorized_keys
  ok "Deploy key generated. Retrieve it (do NOT echo to stdout / logs):"
  echo "  scp root@${DROPLET_IP}:${DEPLOY_KEY_PATH} ./do_ssh_key"
  echo "  # then paste the contents into the GitHub DO_SSH_KEY secret."
else
  warn "Deploy key already exists"
fi

# ─── 13. Enable and start services ───────────────────────────────────────────
step "Starting services"
for svc in apid schedd gatewayd imaged meterd githubd; do
  systemctl enable --now "faas-${svc}.service" 2>/dev/null || true
  ok "faas-${svc}"
done

# ─── 14. Health checks ───────────────────────────────────────────────────────
step "Running health checks"
sleep 3
for svc in apid schedd gatewayd imaged; do
  if systemctl is-active --quiet "faas-${svc}"; then
    ok "faas-${svc} is running"
  else
    warn "faas-${svc} is NOT running — check: journalctl -u faas-${svc} -n 30"
  fi
done

# Quick API check. Probes gatewayd's public listener (not apid directly):
# spec §11 binds apid to loopback-only, so this exercises the full
# gatewayd → apid proxy chain end-to-end (issue #85 PR 1 + PR 2).
if curl -sf http://127.0.0.1:8080/healthz > /dev/null 2>&1; then
  ok "Gateway /healthz OK (apid reachable via loopback proxy)"
else
  warn "Gateway /healthz not responding yet (may need a moment)"
fi

# ─── Done ─────────────────────────────────────────────────────────────────────
echo
echo -e "\033[1;32m═══════════════════════════════════════════════════════════════\033[0m"
echo -e "\033[1;32m  FaaS control plane deployed!\033[0m"
echo -e "\033[1;32m═══════════════════════════════════════════════════════════════\033[0m"
echo
echo "  API:        http://${DROPLET_IP}:8080/v1/apps"
echo "  Dashboard:  http://${DROPLET_IP}:8080/dashboard/"
echo "  Dev token:  cat /root/faas-dev-credentials.txt   (mode 0600, root)"
echo "  Status:     http://${DROPLET_IP}:8080/status"
echo "  Healthz:    http://${DROPLET_IP}:8080/healthz"
echo
echo "  Logs:       journalctl -u 'faas-*' -f"
echo "  Services:   systemctl status 'faas-*'"
echo "  Firewall:   ufw status verbose   (default deny + 22/tcp + 8080/tcp)"
echo
echo "  ⚠ vmmd + builderd are NOT deployed (no /dev/kvm on DO)."
echo "    VM lifecycle operations will return errors — this is expected."
echo
if [[ -f "${DEPLOY_KEY_PATH}" ]]; then
  echo "  📋 GitHub Actions CD setup:"
  echo "     1. Retrieve the deploy key (private key is NEVER printed):"
  echo "          scp root@${DROPLET_IP}:${DEPLOY_KEY_PATH} ./do_ssh_key"
  echo "        then paste its contents into the GitHub DO_SSH_KEY secret."
  echo "     2. Add DO_HOST secret: ${DROPLET_IP}"
  echo "     3. Push to main → auto-deploys"
  echo
fi
