# Continuous app ownership recovery

ADR-421 makes orphaned app ownership converge without a node-change event,
customer request or scheduler restart. Each node-owned scheduler runs an
immediate scan and repeats every five seconds. The work set is stored app
ownership; a process-local cursor rotates bounded pages fairly.

## Runtime behavior

- Default batch: fifty apps, controlled by `FAAS_REBALANCE_MAX_PER_TICK`.
- Successful-transfer cooldown: sixty seconds, controlled by
  `FAAS_REBALANCE_COOLDOWN_SECONDS`.
- Maximum batch time: thirty seconds; maximum store/notification call: five
  seconds. These defaults live in `pkg/api/limits.go`.
- A source must remain non-admitting and destination fully active. Draining,
  maintenance, unavailable, retired and recovering nodes cannot claim apps.
- Capacity refusals advance the periodic cursor and remain eligible on the
  next pass. Capacity, cooldown or database errors never exhaust recovery.

Transfers preserve app status and customer intent. Service recovery uses the
existing bounded app pool and durable replica retries; desired zero remains
stopped. Existing host/inventory controllers establish which old VM residency
is gone. Request apps do not receive an unsolicited wake.

## Inspect convergence

Existing `schedd_rebalance_decisions_total` outcomes include `migrated`,
`conflict`, `no_headroom` and `no_eligibility`. Transfer errors are logged with
app/source/destination IDs. Inspect remaining ownership backlog:

```sql
SELECT a.node_id, count(*) AS orphaned_apps
FROM apps a
WHERE a.node_id IS NOT NULL AND a.status IN ('active', 'evicted_cold')
  AND NOT EXISTS (SELECT 1 FROM compute_nodes n WHERE n.id=a.node_id AND n.active)
GROUP BY a.node_id;
```

Check the service retry ledger and replica gauges with
[continuous service recovery](continuous-service-recovery.md). Full hosts
remain a capacity blocker until physical capacity returns. A lost routing
notification uses the gateway's existing cache refresh behavior.

## Qualify a release

No new migration is needed. ADR-420's service-recovery migration must already
be applied. Portable tests against a disposable Postgres cluster:

```sh
FAAS_PGTEST_TEMPLATE_DATABASE=1 go test -race -count=1 ./pkg/state ./pkg/state/conformance ./pkg/sched \
  -run 'Test(PgStoreConformance|MemStore)/ownership_recovery|TestConformanceCoverage|TestOwnershipRecovery|TestRebalanceOrphanedApps|TestRebalancer'
```

On an isolated native x86_64 Linux KVM fleet, create more service apps than the
configured ownership batch limit. Give each a desired count of two; include
one desired-zero service. Lose their owner host and exhaust surviving
headroom. Confirm old residency is retired by the failure controller and app
ownership remains on the unavailable source while capacity is blocked.
Restart the surviving scheduler, restore headroom, and wait without customer
traffic. Verify every eligible app has an active owner, desired replica counts
converge, public routing serves the new replicas, stopped service stays stopped,
and old reservations/host resources are released. Repeat with node-change
notifications withheld and a destination starting to drain during transfer.

Run `make test-metal` and `make leakcheck` on the supported native hosts and
record release, timings, routing results and resource counts before rollout.
