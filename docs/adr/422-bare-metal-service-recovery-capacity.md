# ADR-422: Bare-metal service recovery capacity

- **Status:** accepted
- **Date:** 2026-10-01
- **Milestone:** M9 managed service recovery
- **Related:** ADR-193, ADR-204, ADR-208, ADR-420, ADR-421

## Problem

Continuous retries and ownership recovery cannot create physical RAM or CPU.
Selling every host's admission capacity leaves services waiting after a host
failure. Bare-metal recovery must reserve existing capacity before accepting
new service intent; provider VM creation is not the recovery mechanism.

## Decision

Add a durable, fleet-wide `service_capacity_policy`. Protection is initially
disabled for compatibility with single-box and already loaded installations.
Enabling is an atomic, validated operation, not a per-daemon environment flag.
The apps, current deployment scopes and resident instance rows are the durable
reservation ledger. No process-local or expiring reservation may release
customer intent during a scheduler restart or database interruption.

The first implementation uses a conservative placement certificate. A service
slot is sized to the largest service replica in the reservation set, separately
for RAM, peak startup CPU and guest vCPU. RAM includes sidecars and the ordinary
VM overhead. CPU includes main startup quota and companion quotas; host CPU
budgets use the existing overcommit factor, not a new performance guarantee.
Include historical resident service shapes until their resources are released.
Instance rows capture RAM/CPU/vCPU admission bounds so plan and configuration
reductions cannot release resources before the old guest is replaced. Legacy
guest vCPU is backfilled at the largest plan topology, conservatively covering
a plan change before migration.

Reserve app desired replicas in the default scope before any deployment or VM
exists, and the same target in every current additional scope. Current means
pending, building, imaging, snapshotting or live. Canary generations share a
scope; live/pending overlap or multiple live generations reserve one additional
rollout slot. Desired zero contributes no future reservation. Accounts remain
conservatively reserved until their service intent is reduced or removed.

For each fully active, freshly heartbeating host, subtract resident ordinary
workloads, jobs and mirrors from its RAM/CPU/vCPU budgets. Count snapshotting,
migrating, warm and draining rows until they release resources. Divide each
remaining budget by the corresponding slot resource and take the smallest
nonnegative integer as the host's slot count. Existing service replicas must
fit their host's slot count. Their total must also fit the declared scope
targets, including the rollout allowance.

The certificate is protected when there are at least two eligible hosts and:

```
reserved service replicas <= sum(host slots) - max(host slots)
```

Every replica fits any slot. Removing any one host leaves at least the full
reservation, with surviving service replicas already occupying valid slots.
Missing replicas therefore fit without moving healthy replicas or assuming
that fragmented RAM and CPU can be combined across machines. This deliberately
rejects some feasible mixed-size packings. The operator report exposes the slot
shape and counts rather than describing the estimate as optimal capacity.

## Atomic enforcement

Postgres statement triggers serialize resource-changing app, deployment and
instance writes on the singleton policy row. Node budget/health changes take
the same lock but may degrade the fleet; hardware failure and operator drain
must remain representable. Plan changes are checked because they change guest
vCPU demand. Disabled statements take shared locks, fencing concurrent enable.

After a resource mutation, reject new/increased service declarations, ordinary
resident growth and excess service replicas unless the resulting certificate
is protected. Existing declared service replicas may spend their reservation
after a failure; still check their host slot capacity. Decreases, releases and
stops remain allowed while degraded. Reject resource growth on ineligible
hosts. Oversized replicas cannot silently change the reserved slot shape.
Promotion of a resident warm or mirror VM also rechecks service membership,
host eligibility and slot capacity even when physical usage is unchanged.
A retained guest topology that enlarges the service slot requires a protected
result; a promotion within the existing shape may use a declared reservation
on a healthy survivor while degraded.
Instance changes that reclassify service residency as ordinary usage must
also preserve recovery headroom. Removing app or deployment intent remains
allowed while degraded, including when its guests are still resident.

The guard covers direct SQL and clients running the previous application
version. The store maps its named constraint to retryable node capacity, and
apid renders `service_recovery_capacity_unavailable` with HTTP 503. Existing
local admission, quota and concurrency checks still apply. Request counters
and unrelated instance telemetry do not take this lock or scan the fleet.

The lock is held only across database work, never VM boot or cloud/provider
calls. Existing multi-row transactions may acquire parent locks first; a
Postgres deadlock aborts one participant atomically and is retryable. This
global serialization and the full resource projection are intentional first
version costs and need native throughput qualification before fleet enable.

## Operator surface and limits

`GET /v1/admin/obs/capacity` and `gregalectl obs capacity` include the aggregate
certificate: disabled, protected, degraded, or needs_hardware. Degraded means
current declared capacity fits but another host loss is not covered;
needs_hardware means the current certificate is insufficient. No raw app IDs
or internal admission maps are exposed. Enable/disable through the validated
store operation or `set_service_capacity_protection(boolean)` in the existing
operator database workflow.

Policy defaults mirror `pkg/api/limits.go` and the existing heartbeat budget:
ordinary VM overhead, CPU overcommit, startup quota, plan guest vCPU and
`ServiceCapacityMinimumHosts=2`. Tests pin the SQL defaults to the Go sources.

This is compute recovery capacity. It does not order servers, fence a
partitioned physical host, guarantee exactly-once execution, or qualify
control-plane and storage durability. Existing failure controllers must retire
old residency before replacement; ADR-420/421 own convergence and routing.

Service reconciliation filters placement candidates using the same healthy
host slot counts and current service occupancy. Physical headroom alone can
repeatedly select a host already full of conservative slots when service
sizes or sustained CPU quotas differ. The projection is a placement hint;
the serialized database guard rechecks the actual admission atomically.
Disabled placement avoids the full capacity projection.

## Validation

Eleven shared MemStore/Postgres tests cover desired intent without VMs, validated
enable/refusal, zero targets,
concurrent writers from different accounts, additional scopes, heterogeneous
hosts, sidecar growth/default CPU, bursts, warm pools, mirrors, oversized/excess
replicas and degraded recovery/stops.
Warm-promotion regressions cover retained guest vCPU after a plan downgrade,
host slots exhausted by smaller replicas, inactive hosts, rollback across all
three state-transition methods, valid promotion during degraded recovery,
warm demotion that preserves headroom and removal of service intent while
degraded.
Postgres additionally covers direct SQL, restarted clients, replay-safe policy
preservation and exclusion of request counters from the admission trigger.
An engine scenario fills the protected limit, loses the owner, restarts the
scheduler/store client and restores desired replicas on the surviving host
without requests, delivered notifications or restored capacity. A desired-zero
service remains stopped. API tests pin refusal codes and aggregate privacy.
Mixed-size host-loss tests verify recovery skips a host full of service slots
even when ordinary placement prefers its remaining physical headroom.

Native x86_64 KVM host-loss/routing acceptance, admission-throughput measurement,
`test-metal` and `leakcheck` are required before enabling on a fleet. Portable
fake-VMM tests do not constitute that qualification.
