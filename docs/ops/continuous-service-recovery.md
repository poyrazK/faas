# Continuous service recovery

Active service apps converge to their desired replicas without incoming
traffic. schedd checks due apps at startup and every five seconds, preserving
retry deadlines through restarts. See [ADR-420](../adr/420-continuous-service-recovery.md).

## Inspect recovery

The scheduler-owned ledger records its last outcome and next attempt. Use an
operator database connection for this read-only query:

```sql
SELECT a.id, a.slug, a.node_id, r.status, r.failures,
       r.next_attempt_at, r.lease_until, r.updated_at
FROM apps a
LEFT JOIN service_recovery r ON r.app_id = a.id
WHERE a.status = 'active' AND a.manifest->>'execution_mode' = 'service'
ORDER BY r.next_attempt_at NULLS FIRST, a.id;
```

| Status | Meaning |
| --- | --- |
| `reconciling` | An attempt owns the lease; a crashed attempt becomes reclaimable after expiry. |
| `ready` | The existing readiness gate passed and serving replica allocations match intent. |
| `starting` / `draining` | Existing transitions still hold managed capacity. |
| `rolling_out` | The existing rollout/handoff controller still has work. |
| `waiting_capacity` | Admission cannot currently fill the desired replicas. |
| `retrying_startup` | A replacement failed to start; inspect its lifecycle events. |
| `waiting_dependency` | State or another reconciliation dependency is unavailable, or no live deployment exists. |
| No row | No attempt has yet claimed this service. |

Healthy checks run every thirty seconds. Failed attempts retry after 5, 10,
20, 40, 80, 160, then at most 300 seconds, indefinitely while the app remains
eligible. Changing desired configuration can reset cooldown on its normal
notification. Setting desired replicas to zero or suspending/holding its account
prevents automatic admission. Stopping a single replica with a positive desired
target permits automatic replacement. A database outage can prevent ledger
updates; inspect
the scheduler's `service recovery:` warnings as well as the last stored row.

The existing `schedd_service_replicas` gauges distinguish desired, ready,
starting, draining and unavailable capacity. Loop work metrics include
`service_recovery` and `service_recovery_sweep` outcomes. A dropped saturated
candidate remains due and is retried by discovery; it does not acquire a lease.

## Qualify a release

Apply migration `20260930211607857_service_recovery.sql` before schedd.
Run portable tests against a disposable Postgres cluster:

```sh
FAAS_PGTEST_TEMPLATE_DATABASE=1 go test -race -count=1 ./pkg/state ./pkg/state/conformance ./pkg/sched \
  -run 'Test(MemStoreConformance|PgStoreConformance)/service_recovery|TestConformanceCoverage|TestServiceRecovery'
```

On an isolated native x86_64 Linux KVM fleet, deploy a service with desired
replicas two. Retire one VM through the normal failure controller, make
replacement admission/startup fail, and confirm a persisted failure/deadline.
Restart schedd before that deadline, restore admission, and wait without
customer requests. Verify exactly two RUNNING replicas, no premature retry,
correct routes and released old host resources. Repeat with desired replicas
zero and verify no new replica. Run the required native lifecycle tests and `make
leakcheck`; record release, timings and results.

This sweep relies on existing inventory/host-failure controllers to establish
which instances are gone. Use [VM inventory recovery qualification](vm-inventory-recovery.md)
for lost-exit detection. Recovery cannot create physical headroom or make an
invalid image pass readiness; its ledger makes those blockers inspectable.
