# ADR-20261109172421 · CPU follows instance size; memory is the only capacity limit

- **Status:** Proposed (owner direction, 2026-11-09)
- **Date:** 2026-11-09
- **Supersedes:**
  - spec §1 row "CPU overcommit 8× (160 vCPU slots), enforced by `schedd`
    admission": becomes a telemetry target (below). The financial model row
    must be updated to match (owner action).
  - spec §4.3 admission rule "vCPU slots (160) not exhausted";
  - spec §4.4 `vcpu_count` per plan (2, Scale 4);
  - ADR-044 decisions 1–3: per-plan sub-slices, per-plan `cpu.weight`, per-plan `cpu.max`;
  - ADR-173: plan RAM/vCPU pairs;
  - ADR-058 Tier A2: the per-node vCPU budget as an admission gate;
  - ADR-20261101160231: CPU and vCPUs follow *plan* RAM. This record makes
    them follow the *instance's* RAM.
- **Amends:**
  - ADR-152: an app's `cpu_millicores` becomes an optional self-imposed cap.
  - ADR-168: the startup boost is moot for uncapped instances.
  - ADR 20261029151111 and ADR 20261030100950: measured CPU and CPU
    pressure decide placement, never admission.
  - ADR 20261027220445: the durable guard checks memory only.
  - ADR-422: service recovery capacity counts memory only.
- **Related:** ADR-909 (admission by measured memory), ADR-005 (cold boot
  always works), ADR-20261106214916 (snapshot refresh).
- **Switches:** schedd runtime config `cpu_admission` = `enforce` | `observe`
  (default `enforce`), and `cpu_size_apps`: app IDs, or `*` for every app,
  whose CPU follows size (default empty: every app on its plan). An earlier
  draft named the second switch `cpu_model` = `plan` | `size`; a list lets
  test apps go first, `*` is the fleet flip, and `cpu_model` already names a
  host's CPU signature (ADR-20261024225612). Defaults and bounds live in
  `pkg/api/limits.go`.

## Context

An instance's CPU is decided by its account's plan, not by its size:

| | Free | Hobby | Pro | Scale |
|---|---|---|---|---|
| Guest vCPUs | 2 | 2 | 2 | 4 |
| Sustained cap (`cpu.max`) | 1 core | 1 core | 1 core | 1 core |
| `cpu.weight` (via a per-plan sub-slice) | 2 | 4 | 8 | 16 |

Measured problems:

- **Extra vCPUs add overhead, not CPU.** Every plan is capped at one core, so
  the second to fourth vCPU only spread the same quota over more threads.
  Each one costs inter-processor interrupts, timer exits and wakeups on every
  request.
  - The prod A/B of 2026-11-07 measured guest CPU per request at 4 / 2 / 1
    vCPUs: Go hello 1,118 / 851 / 523 µs; Node hello 752 / 718 / 645 µs;
    CPU-heavy Node 2,686 / 2,516 / 2,518 µs. p99 was lowest at 1 vCPU.
  - Cold boot and restore were not slower at 1 vCPU (metal bench,
    2026-11-01).
- **The vCPU gate refuses work that fits in memory.**
  - Placement and admission count every instance at its plan's vCPUs against
    `compute_nodes.vcpu_budget`. A 128 MB Scale app books 4 vCPUs.
  - On 2026-11-08 the rc.331 node canary's prime was refused on both nodes:
    "no compute node has 4 guest vCPUs free". The nodes sat at 24/24 vCPUs
    with 2–4 GB of 8.4 GB used.
  - Small instances hit the vCPU budget long before memory: 24 per GCP node,
    about 80 per EX44 at 2 vCPUs each.
- **Same size, different machine.** A 512 MB Hobby instance and a 512 MB
  Scale instance get different vCPUs and different shares of a busy host.
  The product sells memory-time (§4.7); CPU differences by plan are invisible
  in the price and hard to explain.

## Decision

1. **An instance's CPU follows its memory, never its plan.**
   - Guest vCPUs = `max(1, ceil(ram_mb / 1024))`. With today's plan sizes
     that is 1 vCPU up to 1 GB and 2 vCPUs at 2 GB.
   - Plans keep differing in what they allow: largest instance, deployed
     apps, concurrent instances, included GB-hours, idle timeouts and
     features. Two instances of the same size are the same machine on every
     plan.
2. **No per-instance CPU cap.**
   - App instances run with `cpu.max = max`. An instance may use any idle
     host CPU, up to its vCPU count in cores (a guest cannot run more
     threads than it has vCPUs).
   - An app whose `cpu_millicores` is 250 or 500 keeps that as a cap it chose
     (ADR-152). The default, 1000, means uncapped.
   - Builders, jobs, data-repair sandboxes and agent runs keep their own CPU
     rules.
3. **A busy host shares CPU by memory.**
   - Plan priority between instances ends. Until vmmd's cgroup paths stop
     depending on the plan, instances stay in the per-plan sub-slices, which
     all get the same `CPUWeight` (see Rollout 2–3). The end state is every
     app instance directly under `faas-tenant.slice`.
   - Each instance gets `cpu.weight = max(1, ram_mb / 16)`: 8 at 128 MB, 32
     at 512 MB, 128 at 2 GB.
   - Under contention each instance therefore gets at least
     `ram_mb / admission_ceiling_mb` of the tenant CPU. On the reference EX44
     (16 tenant cores, 47.6 GB) that is about 0.34 core per GB. It gets that
     share the moment it wants it, whatever its neighbours burn.
4. **The platform's CPU is protected at the slice, not per instance.**
   - `faas-tenant.slice` gets `CPUQuota = (host threads − platform reserve) ×
     100%`, written per host by `node_join.yml` in `90-host-capacity.conf`.
     Today it is a fixed 1600%, which is no limit on a small host.
   - The platform reserve (default 2 threads, an Ansible variable) keeps
     `gatewayd-public`, `gatewayd-internal`, `vmmd` and `schedd` responsive
     when tenants saturate the host. The load tests show those daemons are
     where most of a request's CPU goes.
5. **Memory is the only capacity limit.**
   - `capacity_unavailable` means only "no node has N MB free".
   - Every CPU refusal becomes a measurement:
     - placement's `node_vcpu` and `node_cpu` gates;
     - `NodeLedger.Admit`'s vCPU and CPU headroom checks;
     - `admission_reserve_node`'s `resource=vcpu` and `resource=cpu`;
     - worker and job node reservations;
     - ADR-422's service recovery capacity.
   - `schedd` still records each instance's vCPUs and CPU in
     `instances.capacity_vcpu` and `capacity_cpu_millicores`.
     `capacity_vcpu` holds the count the instance boots with; the trigger no
     longer floors it at the plan's.
6. **CPU decides where, never whether.**
   - Placement filters nodes by memory, prefers a node that holds the app's
     snapshot, then the node with the least measured CPU pressure (ADR
     20261029151111, 20261030100950).
   - When every node is busy, the wake goes to the least busy one. It may be
     slower; it is never refused.
   - Sustained node saturation alerts operators, to add capacity (§12): the
     `FaasNodeCPUSaturated` alert, on tenant slice throttling and PSI.
7. **More performance means more instances.** One instance offers up to its
   vCPU count in cores. Throughput beyond that comes from scale-out within the
   plan's concurrency, which spreads load across cores and nodes without the
   per-request cost of more vCPUs. Customer-chosen vCPU counts are deferred
   until there is demand and a measurement.
8. **vCPU oversubscription is telemetry.**
   - The new spec §1 value is a target: running guest vCPUs ≤ 16 × host
     threads.
   - It alerts and is reported in `schedd_node_guest_vcpus{node}`; it never
     refuses work. Idle vCPU threads cost nearly nothing, and parked or
     frozen instances have none running.

## Consequences

- **Users.**
  - No app loses sustained CPU: every plan is capped at one core today.
  - Apps up to 1 GB burst to a full core whenever the host has one.
  - 2 GB apps get two cores where Scale gets one today.
  - Light requests get cheaper (Go −53% guest CPU per request from 4 to 1
    vCPU), so more of them fit inside the same share.
  - Paid plans lose priority on a busy host. Equal sizes get equal shares;
    bigger instances get more.
- **Platform.**
  - Density is bound by memory alone: a GCP test node fits about 60
    instances of 128 MB instead of 24; an EX44 about 350.
  - Release canaries and wakes are no longer refused for CPU. A
    CPU-saturated fleet shows up as slower wakes and an alert instead of
    `503 capacity`.
- **Billing is unchanged.** Memory × time (§4.7); idle CPU is free to use.
  Invariant 2 (memory) is unchanged. No other invariant depends on CPU.
- **Snapshots.**
  - A changed vCPU count makes vmmd refuse the old snapshot once
    (`ErrSnapshotVCPUMismatch`). The wake cold-boots through the existing
    fallback (ADR-005).
  - Snapshot refresh re-captures recently active apps in the background:
    `cpu_size_apps` joins `runtimeconfig.CPUShapeKeys`, so a change reruns
    every node's refresh pass, and the pass replays its history to find
    snapshots captured at the old count.
- **Transition.** While instances stay in the per-plan sub-slices, the
  sub-slices share a busy host equally and each divides its share by its
  instances' weights. That is exact within a plan and close across plans
  while one plan holds most instances; it ends with the flat hierarchy.
- **Abuse.** A CPU-burning tenant only gets idle CPU and cannot take another
  instance's share. Sustained high CPU per app is measured, and a guard is
  added only if needed.

## Rollout

Units release independently (docs/release-compatibility.md), so each wire or
schema change ships before its first user.

1. **This ADR.**
2. **vmmd.**
   - Accepts two new optional `AppSpec` fields, `cpu_weight` and
     `cpu_uncapped` (absent keeps today's behavior), on restores and cold
     boots.
   - With them, the jailer scope gets the instance's weight, `cpu.max` is
     `max` and there is no startup boost; the cgroup fence repairs to those
     values. Not sent by schedd yet.
   - The instance stays in its plan's sub-slice: vmmd derives every cgroup
     path, and the plan of a VM recovered after a restart, from the plan.
     Moving to the flat hierarchy needs that derivation changed first (a
     later step).
3. **Deploy.**
   - Per-host tenant `CPUQuota` with the platform reserve, in
     `90-host-capacity.conf`.
   - The four per-plan sub-slices get the same `CPUWeight`, ending plan
     priority.
4. **schedd `cpu_admission = observe`.**
   - CPU gates log and count what they would have refused
     (`schedd_cpu_admission_would_refuse_total{gate}`) and admit on memory.
   - The capacity trigger and `admission_reserve_node` stop refusing on CPU
     (expand/contract migration). Turn it on fleet-wide first: it alone ends
     the canary refusals.
5. **schedd `cpu_size_apps`.**
   - A listed app: vCPUs and weight from `ram_mb`, uncapped `cpu.max`
     (`cpu_uncapped`; vmmd keeps a 250 or 500 millicore choice as a cap,
     also across live CPU policy updates), and a booking of its RAM's CPU
     baseline. It takes precedence over `guest_vcpu_from_cpu_apps`.
   - `capacity_vcpu` holds the booted count (a capacity trigger migration,
     shipped separately).
   - Order: turn snapshot refresh `on`, list the test apps, then set `*`.
6. **Retire.** The per-plan CPU fields in `pkg/api/limits.go` (`VCPU`,
   `CPUWeight`, `CPUQuotaUS`, `CPUPeriodUS`), the plan sub-slices, the
   `guest_vcpu_*` and `app_cpu_*` shape keys, and the vCPU budget gate: one
   release after the flip (`cpu_size_apps = *` in prod 2026-11-10 09:35Z).
   Units release independently, so it lands in this order:
   1. schedd: every app instance's CPU follows its size. `cpu_size_apps`,
      `guest_vcpu_from_cpu_apps`, `guest_vcpu_millicores` and the two
      `app_cpu_*` keys become read-only catalog entries that no daemon
      reads. Snapshot refresh dates a snapshot's vCPU count from its
      instance row (ADR-20261106214916 §2) instead of replaying those keys.
   2. schedd and Postgres: the CPU admission gates go (`cpu_admission`,
      placement's and the ledger's vCPU and CPU budgets,
      `admission_reserve_node`'s CPU resources); CPU stays telemetry.
   3. vmmd drops its plan CPU fallback (plan `cpu.max`, weight and startup
      boost for app instances). It ships a release after 1, once every
      schedd sends `cpu_weight` and `cpu_uncapped`.
   4. The per-plan fields in `pkg/api/limits.go`, the API and CLI fields
      that show them, and the SQL `plan_vcpus` fallbacks.
   5. The flat hierarchy: app instances directly under `faas-tenant.slice`,
      with vmmd still adopting VMs in the old sub-slices for a release.
   6. The dead `FaasCpuStarvation` alert (nothing feeds its throttle
      ratio) and the spec text.
7. **Customer docs and plan pages** ("CPU follows memory: 1 vCPU per GB,
   bursts to idle CPU") once the change is tested on prod.

## Verification

- **Unit.**
  - Placement, `Admit`, node reservations and the durable guard never return
    `capacity_unavailable` for CPU under `observe`.
  - vCPUs and weight are pure functions of `ram_mb`.
  - Uncapped instances get `cpu.max = max`.
  - The capacity trigger keeps the booted vCPU count.
- **Metal.**
  - One CPU-bound instance on an idle host uses its vCPU count of cores.
  - A 128 MB and a 512 MB CPU-bound instance on a saturated tenant slice
    split it about 1:4.
  - `gatewayd` latency stays flat while tenants saturate the slice.
- **Prod.**
  - The node canary primes during a :00 cron wave.
  - Guest CPU per request, p99 and wake latency on the test apps are no worse
    than `plan`.
  - The `FaasNodeCPUSaturated` alert fires on a load test.

## Rejected alternatives

- **Keep CPU by plan.** It refuses work that fits in memory, taxes every
  request with idle vCPUs, and differentiates plans with something customers
  cannot see or measure.
- **CPU and vCPUs by *plan* RAM (ADR-20261101160231 as written).** Still
  plan-dependent: a 128 MB Scale app books a 2 GB plan's CPU.
- **A per-instance cap of one core.** Wastes idle host CPU that costs nothing
  to lend.
- **A per-instance cap proportional to memory (Lambda's model).** It slows
  small apps on an idle host; the 2026-11-02 review rejected it for Free and
  Hobby.
- **Keep the vCPU budget as a refusal at a higher ratio.** Any CPU refusal
  turns a fleet-sizing signal into a customer error.
- **Customer-chosen vCPU counts now.** More vCPUs measured slower per request
  for most apps; deferred until there is demand.

## Open questions

- Platform reserve per host class: 2 threads on GCP test nodes, possibly 4 on
  an EX44.
- The weight constant (`ram_mb / 16`) only sets ratios among tenant
  instances; confirm no other cgroup sibling depends on its absolute value.
- The financial model's §1 "CPU overcommit 8× (160 vCPU slots)" row: the owner
  updates it to the telemetry target, or confirms the model's density math is
  memory-bound.
