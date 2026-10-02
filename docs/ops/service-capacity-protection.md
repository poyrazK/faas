# Bare-metal service capacity protection

ADR-422 and ADR-437 preserve compute capacity for recovery after one host fails. It uses
desired service intent, including pending deployments and rollout overlap,
rather than waiting for VMs to consume physical memory.

## Inspect and enable

Apply migrations through `20261002212534134_service_capacity_projection_jit.sql` first.
Protection defaults to disabled and migration replay preserves the setting.
Inspect `gregalectl obs capacity --json` or the operator capacity API. Its
`service_protection` object includes:

- `state`: disabled, protected, degraded, or needs_hardware.
- `healthy_nodes`: fully active hosts with fresh heartbeats and positive budgets.
- `desired_replicas` and `reserved_replicas`: customer targets and targets plus rollout overlap.
- `replica_ram_mb`, `replica_cpu_millicores`, `replica_vcpu`: conservative recovery slot shape.
- `fleet_slots` and `failover_slots`: slots now and after the largest slot contributor is lost.
- `placements_fit`: current service residency fits its host's slots.
- `legacy_reserved_replicas` and `legacy_placement_capacity`: unrestricted intent and its safe final placement capacity.
- `regions`: sorted named-region certificates with pinned targets, healthy hosts, available/failover slots, legacy residents and stable legacy placement capacity.

Legacy placement capacity includes existing unrestricted residents and pending
intent; it reports a final population.

Disabled snapshots still report all inputs, so compare reserved replicas with
failover slots before enable. Also inspect each selected region and unrestricted
placement capacity: spare slots in another region cannot recover a pinned app,
and unrestricted pending replicas must have a safe final placement. Regions
without pinned reservations need no regional recovery guarantee. Only fully active hosts qualify; drained,
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

New service intent and database writes that consume the protected margin receive
`service_recovery_capacity_unavailable` (HTTP 503). Placement without a safe host
returns a retryable capacity refusal; a pinned request with eligible hosts returns
`compute_region_capacity_exhausted`. Instance admission remains
retryable capacity, not a startup failure. A database outage fails admission
closed; a restart does not release declarations.

Existing declarations remain usable after host loss even when the report is
degraded. Pinned replicas recover only within their selected region. Healthy
unrestricted replicas stay put; lost unrestricted replicas may recover in
another region, provided they leave room for that region's pinned targets.
A shortfall in one region does not block declared recovery in another. Failure controllers retire old rows, ownership recovery transfers
apps, and the service controller fills missing replicas. Reductions and stops
remain allowed. Service placement skips hosts whose recovery slots are full,
including mixed-size services with unused physical headroom. Recovery slots
are conservative: adding a larger service can
change the slot shape for every service and require more physical headroom.
Promoting a warm VM also revalidates its service slots and retained guest size;
unchanged physical usage does not bypass admission. An unsafe promotion leaves
the VM warm and returns retryable capacity. Promotion into an existing declared
slot on a healthy surviving host remains possible while degraded.

Ordinary placement checks the prospective RAM, CPU and guest-vCPU cost against
the certificate before applying affinity (ADR-438). Unrestricted wakes and jobs
can use safe foreign capacity when a preferred region's spare slots are reserved.
Pinned workloads remain local. Mirrors, warm fills and internal app tasks consume
ordinary headroom; normal service wake and prime retain declared recovery slots.
Owner claims allocate no VM and can proceed during degradation. Install migration
`20261002200131931_service_capacity_ordinary_placement.sql` before using the new
scheduler with protection enabled. Downgrading this metadata preserves database
admission protection but causes the new scheduler to refuse ordinary growth until
the metadata is restored. Disabled fleets retain physical placement behavior.

Turning a service VM into an ordinary warm guest must also leave the fleet
protected; removing service intent remains allowed during degradation.
Workers, function floors and jobs have no new service guarantee and cannot
consume the reserve through ordinary admission.

## Qualify before fleet enable

Portable checks against a disposable Postgres cluster:

```sh
FAAS_PGTEST_TEMPLATE_DATABASE=1 go test -race -count=1 ./pkg/state ./pkg/state/conformance ./pkg/sched \
  -run 'Test(MemStoreConformance|PgStoreConformance)/service_capacity|TestRegionalLegacyCapacity|TestServiceCapacityPostgres|TestRegionalServiceCapacityPostgres|TestProtectedServiceRecovery|TestRegionalServiceCapacityPlacement|TestConformanceCoverage'
go test -race -count=1 ./cmd/apid ./cmd/gregalectl \
  -run 'TestServiceCapacityAPI|TestRegionalServiceCapacityAPI|TestWriteObsCapacityHuman'
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

Regional follow-up evidence recorded on 2026-10-02 (macOS ARM64, Go 1.25.13):
full portable state, conformance, scheduler, API, apid and CLI suites pass.
Shared capacity cases pass against disposable Postgres, including regional
intent, mixed legacy placement and recovery, concurrent writers and ordinary
headroom. Exhaustive small-fleet checks validate both the Go and SQL legacy
solvers; candidate hints are compared to the same independent placement oracle.
Direct SQL refusal, restart, populated replay, policy-lock serialization and
guarded downgrade pass. Focused state/scheduler race checks, SQLC v1.31.1 drift,
diff lint with golangci-lint v2.12.2, and Linux amd64 apid/schedd plus metal
acceptance-harness builds pass. Test compilation used `-p=1`,
`-gcflags='all=-l -dwarf=false'` and stripped internal linking. API fixtures used
APFS for subsecond file timestamps. Linux builds retained normal optimization
and omitted debug metadata. These builds are compilation evidence; the native
acceptance harness has not been executed for regional capacity.

This is portable implementation evidence. Native fleet qualification below
is still pending; protection remains opt-in and disabled by default.

Ordinary-placement follow-up evidence recorded on 2026-10-02 (macOS ARM64,
Go 1.25.13): full portable state, conformance and scheduler suites pass. Shared
MemStore/Postgres regressions cover unsafe preferred-region selection, safe
foreign placement, pinned jobs, deployment sidecars, plan guest-vCPU bounds,
normal service recovery, ordinary service-bound residents and owner claims.
An independent exhaustive small-fleet oracle checks prospective slot reductions.
Concurrent stale-hint admission, populated replay, policy-lock serialization
and enabled downgrade/re-upgrade pass. Focused conformance/scheduler capacity
race checks (including Postgres scheduler cases), API capacity tests, migration
policy checks, SQLC v1.31.1 drift and diff lint with golangci-lint v2.12.2 pass.
Linux amd64 apid/schedd and metal-harness builds pass with normal optimization
and stripped debug metadata. These results do not replace native execution.

Internal Linux follow-up on 2026-10-02 (UTC): `gregale-internal-test-1` in
`gregale-prod` ran Go 1.25.13 on Linux amd64 with private Postgres 16. The 151
selected state/scheduler tests and subtests and 60 API/CLI tests and subtests pass
under the race detector without skips. They include the ADR-459 forced-JIT
regression, caller-setting restoration, enabled replay/down/up, concurrent
regional admission and protected recovery. The first diagnostic run exposed
Postgres JIT compilation in the projection; on a fixed fixture, the function
setting reduced calls from 5.17–5.19 seconds to 6.7–8.5 ms with identical JSON
hashes. This small shared-node diagnostic does not establish fleet throughput.

VM substrate checks at commit `58f93a35c` pass on that node: cold boot, 100
park/restore cycles and survival of one service replica after destroying its
peer VM. Namespace and host leak checks pass. These three selected metal tests
use nested KVM; they are Linux runtime evidence, and do not satisfy the dedicated
native multi-host regional recovery/routing qualification or the full metal
suite. The node is excluded from the serving fleet; protection remains opt-in.

ADR-459 disables JIT only within the capacity input function. Other queries
retain the caller's settings. Downgrade resets this function setting and can
reintroduce compiler latency while preserving the admission guard.

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
differently sized hosts and a concurrent destination drain. For regional
qualification, use at least two hosts in the selected region and a foreign
region. Verify abundant foreign capacity does not admit unprotected pinned
intent, pending unrestricted intent has a stable placement, and recovery
retains healthy legacy survivors while restoring pinned replicas locally. Verify stops and
capacity reductions succeed during the incident.

Run `make test-metal` and `make leakcheck` on supported native hosts and record
results before enable. This capacity policy does not establish physical host
fencing or control-plane/storage durability.
