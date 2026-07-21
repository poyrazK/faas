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
  git curl build-essential e2fsprogs jq > /dev/null
ok "Packages installed"

# ─── 2. Go toolchain ─────────────────────────────────────────────────────────
step "Installing Go toolchain"
GO_VERSION="1.25.7"
if ! command -v go &>/dev/null || [[ "$(go version)" != *"go${GO_VERSION}"* ]]; then
  curl -sSfL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xzf -
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
mkdir -p "${FAAS_BIN}" "${FAAS_SRC}" "${CONFIG_DIR}" "${SECRETS_DIR}" \
  "${RUN_DIR}" "${LOG_DIR}" "${SPOOL_DIR}" "${SNAP_DIR}" "${BASE_DIR}"
chown root:faas "${CONFIG_DIR}" "${SECRETS_DIR}"
chmod 0750 "${CONFIG_DIR}" "${SECRETS_DIR}"
chown faas-apid:faas "${LOG_DIR}" "${SPOOL_DIR}"
chmod 0750 "${LOG_DIR}" "${SPOOL_DIR}"
chown faas-imaged:faas "${SNAP_DIR}" "${BASE_DIR}"
chmod 0750 "${SNAP_DIR}" "${BASE_DIR}"
chown faas:faas "${RUN_DIR}"
chmod 0770 "${RUN_DIR}"
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
if ! grep -q 'faas-apid' "${PG_HBA}"; then
  cat >> "${PG_HBA}" <<'EOF'
# FaaS service users → faas database via peer auth (user maps below).
local   faas   faas           peer
local   faas   faas-apid      peer  map=faas_map
local   faas   faas-schedd    peer  map=faas_map
local   faas   faas-imaged    peer  map=faas_map
local   faas   faas-meterd    peer  map=faas_map
EOF
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

# ─── 6. Clone / update source ────────────────────────────────────────────────
step "Fetching source code"
if [[ -d "${FAAS_SRC}/.git" ]]; then
  git -C "${FAAS_SRC}" pull --ff-only
elif [[ ! -d "${FAAS_SRC}" || -z "$(ls -A "${FAAS_SRC}")" ]]; then
  git clone https://github.com/poyrazK/faas.git "${FAAS_SRC}"
else
  warn "Directory ${FAAS_SRC} is not empty and not a git repo — skipping clone"
fi
ok "Source at ${FAAS_SRC}"

# ─── 7. Build binaries ───────────────────────────────────────────────────────
step "Building daemons"
# Stop services first to avoid text file busy on overwrite
for svc in apid schedd gatewayd imaged meterd githubd; do
  systemctl stop "faas-${svc}.service" 2>/dev/null || true
done

cd "${FAAS_SRC}"
make build
mkdir -p "${FAAS_BIN}"
install -m 0755 bin/* "${FAAS_BIN}/"
# Also build the migrate tool
go build -o bin/migrate ./cmd/migrate
install -m 0755 bin/migrate "${FAAS_BIN}/"
ok "Binaries in ${FAAS_BIN}"

# ─── 8. Drop configs ─────────────────────────────────────────────────────────
step "Installing configs"
DO_CONFIG_SRC="${FAAS_SRC}/deploy/digitalocean"

# TOML configs — sed-replace __DROPLET_IP__
for f in "${DO_CONFIG_SRC}/config/"*.toml; do
  base=$(basename "$f")
  sed "s/__DROPLET_IP__/${DROPLET_IP}/g" "$f" > "${CONFIG_DIR}/${base}"
  chown root:faas "${CONFIG_DIR}/${base}"
  chmod 0640 "${CONFIG_DIR}/${base}"
  ok "${base}"
done

# sealed.env
SESSION_KEY=$(openssl rand -hex 32)
sed -e "s/__DROPLET_IP__/${DROPLET_IP}/g" \
    -e "s/__SESSION_KEY__/${SESSION_KEY}/g" \
    "${DO_CONFIG_SRC}/sealed.env.example" > "${SEALED_ENV}"
chown root:faas "${SEALED_ENV}"
chmod 0640 "${SEALED_ENV}"
ok "sealed.env created (session key generated)"

# ─── 9. Systemd units ────────────────────────────────────────────────────────
step "Installing systemd units"
for f in "${DO_CONFIG_SRC}/systemd/"*.{service,slice}; do
  [[ -f "$f" ]] || continue
  cp "$f" /etc/systemd/system/
  ok "$(basename "$f")"
done
systemctl daemon-reload
ok "systemd reloaded"

# ─── 10. Run migrations ──────────────────────────────────────────────────────
step "Running database migrations"
su - faas -s /bin/bash -c "DATABASE_URL='postgres:///faas?host=/run/postgresql&user=faas' ${FAAS_BIN}/migrate"
ok "Migrations applied"

# ─── 11. Generate deploy SSH key ─────────────────────────────────────────────
step "Generating deploy SSH key"
mkdir -p "$(dirname "${DEPLOY_KEY_PATH}")"
if [[ ! -f "${DEPLOY_KEY_PATH}" ]]; then
  ssh-keygen -t ed25519 -N '' -C 'faas-cd-deploy' -f "${DEPLOY_KEY_PATH}"
  # Add to authorized_keys for root
  mkdir -p /root/.ssh && chmod 700 /root/.ssh
  cat "${DEPLOY_KEY_PATH}.pub" >> /root/.ssh/authorized_keys
  chmod 600 /root/.ssh/authorized_keys
  ok "Deploy key generated. Add this PRIVATE key as DO_SSH_KEY secret in GitHub:"
  echo
  cat "${DEPLOY_KEY_PATH}"
  echo
else
  warn "Deploy key already exists"
fi

# ─── 12. Enable and start services ───────────────────────────────────────────
step "Starting services"
for svc in apid schedd gatewayd imaged meterd githubd; do
  systemctl enable --now "faas-${svc}.service" 2>/dev/null || true
  ok "faas-${svc}"
done

# ─── 13. Health checks ───────────────────────────────────────────────────────
step "Running health checks"
sleep 3
for svc in apid schedd gatewayd imaged; do
  if systemctl is-active --quiet "faas-${svc}"; then
    ok "faas-${svc} is running"
  else
    warn "faas-${svc} is NOT running — check: journalctl -u faas-${svc} -n 30"
  fi
done

# Quick API check
if curl -sf http://127.0.0.1:8081/healthz > /dev/null 2>&1; then
  ok "apid /healthz OK"
else
  warn "apid /healthz not responding yet (may need a moment)"
fi

# ─── Done ─────────────────────────────────────────────────────────────────────
echo
echo -e "\033[1;32m═══════════════════════════════════════════════════════════════\033[0m"
echo -e "\033[1;32m  FaaS control plane deployed!\033[0m"
echo -e "\033[1;32m═══════════════════════════════════════════════════════════════\033[0m"
echo
echo "  API:        http://${DROPLET_IP}:8080/v1/apps"
echo "  Dashboard:  http://${DROPLET_IP}:8080/dashboard/"
echo "  Dev token:  fp_dev_localtest"
echo "  Status:     http://${DROPLET_IP}:8080/status"
echo
echo "  Logs:       journalctl -u 'faas-*' -f"
echo "  Services:   systemctl status 'faas-*'"
echo
echo "  ⚠ vmmd + builderd are NOT deployed (no /dev/kvm on DO)."
echo "    VM lifecycle operations will return errors — this is expected."
echo
if [[ -f "${DEPLOY_KEY_PATH}" ]]; then
  echo "  📋 GitHub Actions CD setup:"
  echo "     1. Add DO_SSH_KEY secret (private key printed above)"
  echo "     2. Add DO_HOST secret: ${DROPLET_IP}"
  echo "     3. Push to main → auto-deploys"
  echo
fi
