#!/usr/bin/env bash
# deploy.sh — manual redeploy on the Droplet.
# Usage: sudo bash deploy/digitalocean/deploy.sh
#
# Pulls latest source, rebuilds, runs migrations, restarts services.

set -euo pipefail

FAAS_SRC="/opt/faas/src"
FAAS_BIN="/opt/faas/bin"

step() { echo -e "\n\033[1;36m▸ $1\033[0m"; }
ok()   { echo -e "  \033[1;32m✓ $1\033[0m"; }

step "Pulling latest source"
cd "${FAAS_SRC}"
git pull --ff-only
ok "Source updated"

step "Building daemons"
make build
cp bin/* "${FAAS_BIN}/"
go build -o "${FAAS_BIN}/migrate" ./cmd/migrate
ok "Binaries updated"

step "Running migrations"
su - faas -s /bin/bash -c \
  "DATABASE_URL='postgres:///faas?host=/run/postgresql&user=faas' ${FAAS_BIN}/migrate"
ok "Migrations applied"

step "Restarting services"
for svc in apid schedd gatewayd imaged meterd githubd; do
  systemctl restart "faas-${svc}.service" || true
  ok "faas-${svc} restarted"
done

step "Health checks"
sleep 3
for svc in apid schedd gatewayd imaged; do
  if systemctl is-active --quiet "faas-${svc}"; then
    ok "faas-${svc} is running"
  else
    echo "  ⚠ faas-${svc} is NOT running"
  fi
done

echo -e "\n\033[1;32m✓ Deployment complete\033[0m"
