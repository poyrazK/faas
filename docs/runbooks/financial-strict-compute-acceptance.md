# Strict app compute native acceptance

This candidate is local and unpublished. Use dedicated native x86_64 Linux KVM.

```sh
set -euo pipefail
test "$(uname -m)" = x86_64
test -c /dev/kvm
make test-metal RUN_REGEX='^TestMetalFinancialBudget'
make leakcheck
```

Apply additive migrations and deploy matching scheduler, gateway, vmmd and meter.
Enable native process recovery and confirm current financial owners on all nodes.

1. Activate app-scoped strict gross compute `suspend_workloads`, zero drain,
   empty alert thresholds. Refuse busy apps, incomplete evidence and old owners.
2. Race cold starts across two nodes and deployments. Check all policy limits,
   fenced physical attempts and retained queued work. Other apps must continue.
3. Exercise cold/restore, paused warm pools, frozen thaw, workers, commands,
   retries and deployment changes. Unsupported qualification restore/job paths
   must refuse strict app work before physical effects.
4. Disconnect scheduler/meter/database after granting funding. Check local expiry
   and cancellation of boot in flight. Inject failed kill and failed settlement;
   retain funding/capacity, retry exact physical cleanup and settle exactly once.
5. Kill/restart vmmd during boot, live execution and teardown. Check native
   quarantine and exact-generation recovery. Stale RUNNING rows cannot bill
   past funding; Gregale absorbs unfunded tail consumption.
6. Replay acquire/renew/settle and stale incarnation messages. Test manual and
   next-period holds across UTC month reset. Disable/raise a policy and attempt
   a limit below existing funding. Preserve independent payment/security holds.
   Clean up a denied or unstarted wake without a VM journal, restart the node
   incarnation, then replay its persisted wake ID or command lease token. It
   must remain fenced before native allocation. Check the teardown pause at
   the UTC boundary before initializing the next month's funding.
   Edit a held policy before that initialization: release its own hold while
   preserving prior funding and the next acquire's accounting/retirement checks.
7. Reconcile billed compute with funded ceilings, sidecars, rounding, prices,
   corrections and synthetic floor waiver. Verify API/CLI/SDK/console balances
   and distinguish commitments from invoice spending.
8. Measure warm and cold admission latency/throughput against platform targets.

Run the full native VM lifecycle suite and leakcheck afterwards. Record host
identity, commit hashes, commands, results and resource inventory in the release
review. The current managed cloud container has no native acceptance path.
