# ADR-20261101160231: Apps get one guest vCPU per 1,000 sustained millicores

- **Status:** proposed
- **Date:** 2026-11-01
- **Amends:** ADR-173 (plan RAM/vCPU pairs) and ADR-152 (vCPU count stays
  plan-derived), and spec §4.4 (`vcpu_count` = 2, Scale 4). Snapshot
  compatibility follows the pattern of ADR-20261024225612 (CPU templates).
- **Decision:**
  1. **An app instance boots `max(1, ceil(cpu_millicores / 1000))` guest
     vCPUs.** Every shape sold today (250, 500 and 1,000 millicores, ADR-152)
     gets one vCPU, on every plan including Scale. A future shape above
     1,000 millicores gets more vCPUs with it. The rule covers every place
     schedd now sends the plan's `limits.VCPU` for an app: wakes, cold boots,
     deployment prime boots, app tasks and forks. Builder VMs (ADR-791), jobs
     (already one vCPU), agent runs and managed Postgres keep their own shapes.
  2. **Snapshots record their vCPU count.** vmmd reports it with each capture,
     beside `cpu_template` and `mem_bytes`. imaged stores it in a new nullable
     `snapshots.vcpu_count` (CHECK 1–64). schedd's snapshot check gains the cold
     reason `vcpu_mismatch`: a row captured at another count is retired like a
     RAM mismatch, and the wake cold-boots (ADR-005). A legacy row (NULL)
     counts as the plan's old topology (2, Scale 4). Each app therefore pays
     one cold boot on its first wake after the change, then snapshots again.
  3. **Admission books the derived count.** vCPU slots per node are unchanged.
     Under measured CPU admission (ADR-20261029151111, on by default) guest
     vCPUs already stop counting at the gate, but bookings still count toward
     its 700% cap; there each running app books half of what it books today
     (a quarter on Scale).
  4. **Customer surface follows at launch, not before.** `vcpu` and
     `effective_limits.guest_vcpus` on apps and account limits,
     `api.VCPUPerPlan`, the CLI and the docs report the derived count only once
     the change is on for everyone (owner rule: no customer-facing change before
     a production test). For one release after that, the create-time `vcpu`
     assertion accepts both the old plan value and the derived one, so pinned
     clients get no `invalid_cpu_ram_pair` 422.
  5. **Switch:** schedd `FAAS_GUEST_VCPU_FROM_CPU_APPS`, a comma list of app
     IDs or `*`. Empty (the default) keeps today's plan topology. A list of
     apps, not nodes, lets two identical apps on the same node be compared at
     the same time.
- **Why:** guest-VM CPU is the largest per-request cost on a compute node.
  At the 2026-10-31 ceiling (~500 req/s), fsn-3 spent 1.0–1.1 cores on
  Firecracker alone for its three busiest apps. The second vCPU does
  not raise sustained CPU, since `cpu.max` caps every sold shape at one CPU
  (ADR-152). Measured on 2026-11-01 (`TestMetalRequestCostBench`,
  `pkg/fcvm/request_cost_bench_metal_test.go`).
  - **Setup:** gregale-internal-test-1, a 2-vCPU GCP VM with nested KVM like
    production, running Firecracker 1.17.0 and guest kernel 6.18.54 as
    production does. The apps are a static Go `net/http` hello server, and a
    Node 22 hello server on the production node runner base with the current
    guest-init. One client sends requests in series, 3,000 per mode, after
    the host is under 8% busy.
  - **Cost:** Firecracker CPU per request (vCPU threads plus the VMM thread):

    | App | Connection | 1 vCPU | 2 vCPUs | 4 vCPUs |
    |---|---|---:|---:|---:|
    | Go | reused | 387 µs | 733 µs | 1,100 µs |
    | Go | new per request | 977 µs | 1,413 µs | 1,837 µs |
    | Node | reused | 477 µs | 593 µs | — |
    | Node | new per request | 1,207 µs | 1,377 µs | — |

    Serial latency follows CPU: 383, 732 and 1,087 µs for Go on a reused
    connection. Four vCPUs overcommit this 2-vCPU host, so that column
    overstates what a 4-vCPU production node would see. The bridge pools guest connections, so the reused rows are
    production's path.
  - **Where it goes:** a bpftrace of KVM exits by reason (Go, same host,
    Firecracker 1.7.0 and guest kernel 6.1.134). The second vCPU raises guest MSR writes from 4.3 to
    11.7 per request, and each takes 12.8 µs instead of 4.6. Those writes are
    interrupts between vCPUs and LAPIC timer programming; the guest boots with
    `lapic=notscdeadline`, ADR-642. Halts go from 1.8 to 5.1 per request, and
    KVM's own kernel time from 70 to 227 µs. Under nested virtualization each
    of these exits passes through the host hypervisor first. Go's scheduler
    wakes the idle vCPU on most requests; Node does so less often, which is why
    it gains less.
  - **Wake latency does not depend on the vCPU count.** ADR-173 kept the plan
    topology to protect cold-wake latency.
    - Restore wall time (`TestMetalRestoreTimingBench` with its busybox
      fixture, 20 cycles, 256 MiB, p50/p95): 272/304 ms at one vCPU, 268/357
      at two, 276/334 at four.
    - Cold boot, median of four after a cache-warming boot: Node 2.0 s at one
      vCPU and 2.05 s at two, Go 1.32 s and 1.41 s.
- **Consequences:**
  - **Apps see one CPU.** `nproc`, `os.cpus()` and `runtime.NumCPU()` report
    1, Go's `GOMAXPROCS` becomes 1, and worker pools sized from the CPU count
    shrink (which also lowers their RAM). Sustained throughput is unchanged,
    because the quota already caps at one CPU. But a burst that ran on two
    vCPUs within one `cpu.max` period now queues on one, so a CPU-bound
    request mix can see higher tail latency. The A/B includes such an app.
  - **guest-init shares the vCPU with the app.** It already shares the
    cgroup quota. Readiness and liveness answers depend on the guest
    scheduler preempting a busy app, and the A/B checks liveness under a
    CPU-bound load.
  - **One cold boot per app** after the switch includes it, when its old
    snapshot is retired.
  - **Lower measured CPU per request** leaves measured CPU admission more room
    on the same node. Fewer exits also means less work for the host kernel
    under load.
  - **Mixed versions:** `snapshots.vcpu_count` is an expand-only, nullable
    column. An imaged that predates it writes NULL, which reads as legacy and
    is correct while the switch is empty. vmmd, imaged and schedd must all
    record and check the count before any app is listed.
- **Rollout:**
  1. Benches: `TestMetalRequestCostBench`, and `FAAS_RESTORE_BENCH_VCPUS` on
     `TestMetalRestoreTimingBench` (this ADR's PR).
  2. `snapshots.vcpu_count` (migration), vmmd reports it, imaged stores it,
     schedd rejects a mismatch. This step is inert while no app is listed:
     every capture records the count it booted with, which equals the plan's.
  3. schedd's `FAAS_GUEST_VCPU_FROM_CPU_APPS`; admission books the derived
     count.
  4. Production A/B on one node: two identical apps each for Go and Node, one
     of each listed, plus one CPU-bound app. Run the 300 → 600 req/s step
     test. Compare cgroup CPU per request, p50/p99 latency,
     `gateway_wake_latency_seconds`, cold boot time and liveness.
  5. If the A/B confirms it (owner decision): set `*`, and change the API,
     CLI, docs and spec §4.4 in the same release. The switch is removed a
     release later.
- **Rejected alternatives:**
  - Keep two vCPUs and tune the guest (paravirtual IPIs, halt polling, timer
    settings). The extra exits come with having a second vCPU. Tuning moves
    some of the cost but does not remove it, and the second vCPU still buys no
    sustained CPU at a one-CPU quota.
  - Pin the app to one vCPU inside the guest. The guest still runs the second
    vCPU's timers and housekeeping, and the app sees two CPUs it cannot use.
  - Fewer exits per interrupt through virtio-pci with MSI-X instead of
    virtio-mmio. Acknowledging interrupts costs about four userspace MMIO
    exits per reused-connection request, about 15% of its cost. That is
    worth its own ADR and is independent of this one.
- **Open questions for the owner:**
  1. Scale apps boot four vCPUs at a 1,000-millicore quota, 2.8× the
     one-vCPU cost per request above (on an overcommitted test host). Proposed: the same rule (one vCPU).
     Alternative: keep four until a shape above 1,000 millicores exists.
  2. Plan pages and docs present guest vCPUs as a plan property
     (`gregale deploy --vcpu`, `effective_limits.guest_vcpus`). Step 5
     changes what they say.
