# Monitored preview stop acceptance (ADR-566)

This is a local implementation candidate. Do not promote preview stopping from
merged code alone. The notification milestone is a separate dependency.

## Required native evidence

Use the dedicated native x86_64 Linux KVM acceptance host, its existing approved
kernel/image assets, and an isolated migrated PostgreSQL database. Nested
virtualization and a skipped metal test do not count as acceptance.

```bash
set -euo pipefail
test -c /dev/kvm
test "$(uname -m)" = x86_64
RUN_REGEX='^TestFinancialBudgetPreviewStopMetal$' make test-metal PKGS=./cmd/e2e
make leakcheck
```

The native test warms production and preview VMs, crosses a retained threshold,
checks immediate warm admission refusal, waits for actual teardown, checks cold
admission refusal, and releases only the budget revision before waking again.
Also run the existing full VM lifecycle acceptance suite for the release.

Exercise request/service/worker/job previews, paused warm pools, frozen VMs,
minimum-instance floors, cron/retry/redeploy paths and long-running command VMs.
Confirm per-target resident counts include commands and do not become enforced
on failed destroy, failed settlement or an in-flight preparation RPC. Restart
meterd/schedd and interrupt PostgreSQL/vmmd during draining; recover from retained
rows. Verify memory and CPU reservations survive scheduler restart. Unknown
legacy command ownership must remain failed/unavailable, never enforced.

## Rollout

Apply all additive migrations, then deploy the matching scheduler and gateways,
meterd, apid, and finally the console in that order. Drain legacy command sessions
before enabling this candidate.
Verify metering freshness and delivery independently of stopping. Exercise a
small preview budget with an explicit drain and resume rule; production should
continue, and the customer UI must retain the monitored-overshoot explanation.

Check manual holds across a UTC month and next-period expiry. Check overlapping
budgets and payment/security/user holds: changing one policy releases only its
own revision. Queued work keeps its attempt count until actual dispatch; its
existing customer-selected expiration/cancellation policy remains effective.

Disable new stopping policies before rollback. Preserve financial decisions,
audit and runtime ownership rows. Already-running teardown calls may finish.
Use a compatible scheduler/gateway until retained holds are explicitly released;
an old binary that ignores holds is not a safe enforcement rollback.
