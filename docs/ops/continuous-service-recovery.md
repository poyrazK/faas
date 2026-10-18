# Continuous service and worker recovery

Active service apps converge to their desired replicas without incoming
traffic. schedd checks due apps at startup and every five seconds, preserving
retry deadlines through restarts. See [ADR-420](../adr/420-continuous-service-recovery.md).

Worker pools use a separate durable `worker_recovery` ledger. schedd discovers
due owned apps at startup and every five seconds, including captured worker
environments and unfinished cleanup. Capacity refusal and startup failures retry
without another deploy or queue event. See [durable worker recovery](../adr/20261017221000-durable-worker-recovery.md)
and [recovery diagnostics](../adr/20261017223000-worker-recovery-diagnostics.md).

## Inspect worker recovery

Run `gregale inspect <app>` or `gregale inspect <app> --json`. Current operations
shows the last app-wide recovery outcome, failure count and next attempt, then
each environment's desired/running/starting/draining workers with its observation
time. Desired is the scheduler's target after demand, stabilization and cooldowns,
before account and node admission. Counts are observations, not a fresh readiness
probe. Stopping or predecessor-generation residents count as draining.

The same evidence is in `GET /v1/apps/{slug}/operational-summary` under
`recovery.workers`. Use `?environment=staging` or `?scope=default` to select pool
observations. The status, failures and retry deadline remain **app-wide**. A
healthy staging pool may appear beside an app-wide capacity refusal in another
environment. `eligible=false` means the current app/account does not permit
automatic admission. A lease deadline shows an in-progress or expired claim;
claim tokens are never returned.

An omitted worker object can mean an older server or a request-only app. A worker
with no ledger returns `not_observed`; a failed read returns `available=false`
with `status=unknown`. No pool observations for a selection means its counts are
unknown. Observations older than fifteen minutes show `stale=true`; their last
values remain visible with their original timestamp. A new desired revision
invalidates old observations. Reading status never schedules or wakes workers.

For platform investigation with an operator database connection, list active apps
and their ledgers, including apps that have never been claimed:

```sql
SELECT a.id, a.slug, a.node_id, r.status, r.failures,
       r.next_attempt_at, r.lease_until, r.updated_at, r.pools
FROM apps a
LEFT JOIN worker_recovery r ON r.app_id = a.id
WHERE a.status = 'active'
ORDER BY GREATEST(r.next_attempt_at, r.lease_until) NULLS FIRST, a.id;
```

## Worker alerts

All three alerts are internal warnings and link to this runbook:

| Alert | Meaning and first checks |
| --- | --- |
| `FaasWorkerPoolUnderCapacity` | Fresh observations show a deficit for ten minutes. Inspect app status for `waiting_capacity`, `retrying_startup` or `waiting_dependency`. Check node/account headroom, boot events and image/runtime inputs. Restore capacity or correct the startup failure; automatic retries continue. |
| `FaasWorkerRecoveryStalled` | A retry and lease are both overdue by at least sixty seconds for two minutes. Check schedd ownership, work saturation, database access and recovery logs. A future retry or active lease is not overdue. |
| `FaasWorkerRecoveryObservationUnavailable` | An eligible app lacks a fresh pool observation, or successful database collection is older than sixty seconds, for five minutes. Check deployment availability, state reads, scheduler version and `worker recovery: collect diagnostics` warnings. Counts are unknown until observations resume. |

The scheduler exports owner-scoped aggregate `schedd_worker_recovery_apps`
conditions (`eligible`, `unknown`, `overdue`), `schedd_worker_recovery_replicas`
states (`desired`, `running`, `starting`, `draining`, `missing`) and
`schedd_worker_recovery_observed_timestamp_seconds`. They have no customer or
environment labels. Locate affected apps through the API or the read-only query
above. Deficits are summed per environment; surplus in a neighbor cannot hide
missing workers. Intentional zero targets contribute no deficit. Inactive apps,
suspended/deleted accounts and abuse holds are excluded. Missing/stale plans are
unknown even when another environment is fresh.

Capacity/stall alerts require a collection within sixty seconds. If database
collection fails, its previous timestamp ages and the unavailable alert takes
over. Existing schedd scrape/down alerts cover a missing exporter. Restarts load
observations and deadlines from Postgres; successful collections replace counts
with zero after recovery, parking or loss of ownership. Alert timers retain their
normal Prometheus semantics when the same scrape target restarts.

## Inspect service recovery

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
For workers, also apply `20261017221000000_worker_recovery.sql` and
`20261017223000000_worker_recovery_observations.sql` before upgrading schedd.
Leave the additive observation column in place during an older-binary rollback;
older schedulers do not refresh it, so its evidence eventually becomes unknown.
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

For worker diagnostics, run the shared `worker_recovery` store contracts, the
scheduler's `TestWorkerRecovery` restart/scope tests, the API worker-diagnostics
tests, and CLI inspect/watch tests. Validate the alert evaluator with:

```sh
promtool check rules deploy/ansible/roles/prometheus/files/faas.rules.yml
promtool test rules pkg/promqlrules/testdata/worker_recovery.test.yml
```

In native worker qualification, refuse admission until an app reports
`waiting_capacity`, required workers remain missing and the capacity alert fires.
Restart schedd, verify the same deadline and observations, restore capacity and
wait without sending new work. Verify the target generation and counts recover,
the next collection reports zero missing capacity, and the alert resolves. Repeat
at desired zero and with separate environment demand. Preserve the existing
native runtime and leak-check evidence required by the worker recovery ADR.
