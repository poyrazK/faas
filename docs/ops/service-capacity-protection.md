# Bare-metal service capacity protection

ADR-422 preserves compute capacity for recovery after one host fails. It uses
desired service intent, including pending deployments and rollout overlap,
rather than waiting for VMs to consume physical memory.

## Inspect and enable

Apply migration `20261001084654053_service_capacity_protection.sql` first.
Protection defaults to disabled and migration replay preserves the setting.
Inspect `gregalectl obs capacity --json` or the operator capacity API. Its
`service_protection` object includes:

- `state`: disabled, protected, degraded, or needs_hardware.
- `healthy_nodes`: fully active hosts with fresh heartbeats and positive budgets.
- `desired_replicas` and `reserved_replicas`: customer targets and targets plus rollout overlap.
- `replica_ram_mb`, `replica_cpu_millicores`, `replica_vcpu`: conservative recovery slot shape.
- `fleet_slots` and `failover_slots`: slots now and after the largest slot contributor is lost.
- `placements_fit`: current service residency fits its host's slots.

Disabled snapshots still report all inputs, so compare reserved replicas with
failover slots before enable. Only fully active hosts qualify; drained,
recovering, unavailable, retired and stale hosts do not supply headroom.

Use the existing protected operator database connection:

```sql
SELECT set_service_capacity_protection(true);
```

This locks the fleet admission boundary and validates current capacity. A
failure rolls back enable. Add or recover bare-metal capacity, or reduce
desired service targets, before trying again. Do not directly change policy
resource constants: they mirror the versioned admission model.

To disable the policy:

```sql
SELECT set_service_capacity_protection(false);
```

Disabling permits ordinary admissions to use recovery headroom. It does not
change replica targets or stop services.

## Behavior during incidents

New service capacity and bursts that consume the protected margin receive
`service_recovery_capacity_unavailable` (HTTP 503). Instance admission remains
retryable capacity, not a startup failure. A database outage fails admission
closed; a restart does not release declarations.

Existing declarations remain usable after host loss even when the report is
degraded. Failure controllers retire old rows, ownership recovery transfers
apps, and the service controller fills missing replicas. Reductions and stops
remain allowed. Service placement skips hosts whose recovery slots are full,
including mixed-size services with unused physical headroom. Recovery slots
are conservative: adding a larger service can
change the slot shape for every service and require more physical headroom.
Promoting a warm VM also revalidates its service slots and retained guest size;
unchanged physical usage does not bypass admission. An unsafe promotion leaves
the VM warm and returns retryable capacity. Promotion into an existing declared
slot on a healthy surviving host remains possible while degraded.
Turning a service VM into an ordinary warm guest must also leave the fleet
protected; removing service intent remains allowed during degradation.
Workers, function floors and jobs have no new service guarantee and cannot
consume the reserve through ordinary admission.

## Qualify before fleet enable

Portable checks against a disposable Postgres cluster:

```sh
FAAS_PGTEST_TEMPLATE_DATABASE=1 go test -race -count=1 ./pkg/state ./pkg/state/conformance ./pkg/sched \
  -run 'Test(MemStoreConformance|PgStoreConformance)/service_capacity|TestServiceCapacityPostgres|TestProtectedServiceRecovery|TestConformanceCoverage'
go test -race -count=1 ./cmd/apid ./cmd/gregalectl \
  -run 'TestServiceCapacityAPI|TestWriteObsCapacityHuman'
```

Local evidence recorded on 2026-10-01 (macOS ARM64, Go 1.25.13): all eleven
shared capacity cases pass against MemStore and disposable Postgres, as do
direct SQL/restart/migration replay, protected-limit recovery and mixed-size
host-loss recovery. Existing Postgres service/ownership recovery and node
reservation regressions also pass with the new migration.
The four warm-transition follow-up cases reproduced failures in both stores
before the fix and pass afterward, including preservation of the instance row
and capacity certificate on refusal.
The follow-up reran the full portable state, conformance and scheduler race
suites, the Postgres admission/ownership regressions and both Postgres
protected-service recovery scenarios.
Pinned v2.4.0 lint over state/scheduler sources and tests, SQL generation,
schema/migration consistency and repository policy checks also pass.

The full portable race suites for `pkg/state`, `pkg/state/conformance`,
`pkg/sched`, `cmd/apid`, `cmd/gregalectl` and `pkg/api` pass. Those runs used
`FAAS_SKIP_PG_TESTS=1`, `-vet=off`, `-p 1`, `-gcflags=all=-dwarf=false` and
`-ldflags=-s`; the focused Postgres runs used the same Go flags without
skipping Postgres. Pinned golangci-lint v2.4.0 reports zero issues, sqlc
v1.31.1 drift and repository policy checks pass, and schedd/apid/gregalectl
builds pass. Debug metadata was stripped to limit local disk usage.

This is portable implementation evidence. Native fleet qualification below
is still pending; protection remains opt-in and disabled by default.

On an isolated native x86_64 Linux KVM fleet, measure admission throughput with
protection enabled and disabled at representative workload counts. Record
lock wait/transaction latency and confirm request-counter updates do not take
the policy lock. The initial implementation serializes resource mutations and
performs full resource projections; do not infer production throughput from
portable tests.

Fill to the protected limit, verify the next service increase and ordinary
burst are refused, then lose a compute owner. Restart the surviving scheduler
and withhold node/app notification delivery. Without changing admission
ceilings or generating customer traffic, verify desired services recover,
desired-zero remains stopped, public routing serves surviving replicas, old
host reservations are retired, and the report becomes degraded. Repeat with
differently sized hosts and a concurrent destination drain. Verify stops and
capacity reductions succeed during the incident.

Run `make test-metal` and `make leakcheck` on supported native hosts and record
results before enable. This capacity policy does not establish physical host
fencing or control-plane/storage durability.
