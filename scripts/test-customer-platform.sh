#!/bin/sh
set -eu

: "${DATABASE_URL:?Set DATABASE_URL to a disposable Gregale test database}"
: "${CUSTOMER_DATABASE_URL:?Set CUSTOMER_DATABASE_URL to a disposable application database with a non-superuser role}"
task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
starter_dir=$(mktemp -d "${TMPDIR:-/tmp}/gregale-customer-platform.XXXXXX")
trap 'rm -rf "$starter_dir"' EXIT HUP INT TERM
cp -R "$task_root/cmd/gregale/templates/customer-platform/." "$starter_dir/"
# Keep node_modules out of the Go embed tree.
(cd "$starter_dir" && npm ci --ignore-scripts && npm test && npm run test:postgres)
export GREGALE_CUSTOMER_PLATFORM_DIR="$starter_dir"
cd "$task_root"
"${GO:-go}" test -p 1 ./pkg/state -count=1 \
  -run 'TestPg(CreateAppPersistsPlatformTenantRequired|ProjectPlatformTenantPolicyLifecycle|ApplyProjectPlanPersistsPlatformTenantPolicy|PlatformTenantCrossAppLifecycle)$'
"${GO:-go}" test -p 1 ./cmd/apid -count=1 \
  -run '^TestCustomerPlatformStarterTwoCustomerAcceptance$'
