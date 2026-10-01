#!/bin/sh
set -eu

: "${DATABASE_URL:?Set DATABASE_URL to a disposable Gregale test database}"
: "${CUSTOMER_MIGRATION_DATABASE_URL:?Set CUSTOMER_MIGRATION_DATABASE_URL to the disposable schema owner connection}"
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
  -run 'Test(MemPlatformTenantInvocationLifecycle|PgPlatformTenantInvocationLifecycle|PgPlatformTenantInvocationIdentityImmutable|PgCreateAppPersistsPlatformTenantRequired|PgProjectPlatformTenantPolicyLifecycle|PgApplyProjectPlanPersistsPlatformTenantPolicy|PgPlatformTenantCrossAppLifecycle)$'
"${GO:-go}" test -p 1 ./cmd/apid -count=1 \
  -run '^Test(CustomerPlatformStarterTwoCustomerAcceptance|PlatformTenantInvocationSelfService)$'

"${GO:-go}" test -p 1 ./pkg/gateway ./cmd/gatewayd-internal -count=1 \
  -run 'Test(ApplyEdgeRuleAsync|AsyncRoute|SynthAdapter)'
"${GO:-go}" test -p 1 ./pkg/sched -count=1 \
  -run '^Test(HTTPGatewaySynth|Drain_PlatformTenantSuspensionAndRetry)'

"${GO:-go}" test -p 1 ./pkg/auth/middleware -count=1 \
  -run 'Test(PlatformTenantInvocationPathsAllowed|RequireSession_PlatformTenantAccessTokenIsTenantSelfOnly)'
