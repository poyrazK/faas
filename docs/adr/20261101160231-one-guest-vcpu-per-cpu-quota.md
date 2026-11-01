# ADR-20261101160231: CPU follows plan RAM, and apps get one guest vCPU per core

- **Status:** proposed
- **Date:** 2026-11-01 (CPU baseline and ceiling added 2026-11-02)
- **Amends:** ADR-173 (plan RAM/vCPU pairs), ADR-152 (vCPU count stays
  plan-derived; app CPU shapes), ADR-044 (per-plan `cpu.max`), the flat
  1,000-millicore booking under CPU admission (ADR-20261029151111), and
  spec §4.4 (`vcpu_count` = 2, Scale 4). The restore guard reuses ADR-005's
  cold-boot fallback and schedd's stale marking.
- **Decision:**
  1. **CPU follows plan RAM: a baseline plus a burst ceiling** (owner,
     2026-11-02).

     | | Free 256 MB | Hobby 512 MB | Pro 1 GB | Scale 2 GB |
     |---|---:|---:|---:|---:|
     | Baseline: booked, and the share when the node is busy | 250 m | 500 m | 1,000 m | 2,000 m |
     | Ceiling (`cpu.max`) | 1,000 m | 1,000 m | 1,000 m | 2,000 m |
     | Guest vCPUs | 1 | 1 | 1 | 2 |

     - The baseline is 1,000 millicores per GB of plan RAM. Admission books
       it in place of today's flat 1,000. `cpu.weight` already follows RAM
       (2, 4, 8 and 16, ADR-044) and is unchanged, so the baseline is also
       each instance's share when the node is busy.
     - The ceiling is `max(1,000, baseline)`. No plan's ceiling drops below
       today's one core, and Scale gets a second core. An app's own lower
       `cpu_millicores` (ADR-152) still lowers its ceiling, and it then books
       the smaller of the two.
     - Cold boot and restore keep the startup boost of at least one core, so
       wake time does not change on Free or Hobby.
  2. **An app instance boots `max(1, ceil(ceiling / 1000))` guest vCPUs:**
     one on Free, Hobby and Pro, two on Scale. A future ceiling above
     2,000 millicores gets more vCPUs with it. The rule covers every place
     schedd now sends the plan's `limits.VCPU` for an app: wakes, cold boots,
     deployment prime boots, app tasks and forks. Builder VMs (ADR-791), jobs
     (already one vCPU), agent runs and managed Postgres keep their own shapes.
  3. **vmmd refuses a restore at another vCPU count.** Firecracker restores
     the count a snapshot was captured with, whatever the wake asks for. Right
     after `/snapshot/load`, before the resume hook and readiness, vmmd counts
     the restored process's `fc_vcpu` threads. If that differs from the
     wake's count it fails the restore with `ErrSnapshotVCPUMismatch`. The
     wake cold-boots through the existing fallback, and schedd marks the
     snapshot stale, because the error is not node-local. Each app therefore
     pays one wasted restore and one cold boot on its first wake after its
     count changes, then snapshots again. A warm-pool or migration restore
     fails instead of cold-booting, as for any restore error there.
  4. **Admission books the baseline and the derived vCPU count.** vCPU slots
     per node are unchanged. Under measured CPU admission
     (ADR-20261029151111, on by default) guest vCPUs already stop counting at
     the gate, but bookings still count toward its 700% cap. There a running
     app books half of today's vCPUs (Scale also half: two of four), and a
     quarter (Free) or half (Hobby) of today's millicores.
  5. **Customer surface follows at launch, not before.** Plan pages list RAM,
     the CPU baseline and burst ceiling, and instances per app; guest vCPUs
     stop being a plan line. `vcpu` and `effective_limits.guest_vcpus` on apps
     and account limits, `api.VCPUPerPlan`, the CLI and the docs report the
     derived count only once the change is on for everyone (owner rule: no
     customer-facing change before a production test). For one release after
     that, the create-time `vcpu` assertion accepts both the old plan value
     and the derived one, so pinned clients get no `invalid_cpu_ram_pair` 422.
  6. **Switch:** schedd `FAAS_GUEST_VCPU_FROM_CPU_APPS`, a comma list of app
     IDs or `*`. Empty (the default) keeps today's plan topology. A list of
     apps, not nodes, lets two identical apps on the same node be compared at
     the same time. The same list later applies the baseline and ceiling.
- **Why CPU follows RAM:** billing is plan RAM per running second (§4.7),
  but today every app books one core and may use one core whatever its RAM.
  So one billed GB-second carried four cores' worth of ceiling on Free and
  half a core on Scale. Bookings decide how many instances fit on a node
  under CPU admission, and a 256 MB instance used as much of that budget as
  a 2 GB one. The busy-node share (`cpu.weight`) already followed RAM; the
  booking and the Scale ceiling did not. A ceiling of at least one core
  keeps small plans as fast as today on a quiet node.
- **Why fewer vCPUs:** guest-VM CPU is the largest per-request cost on a compute node.
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
  - **Free, Hobby and Pro apps see one CPU.** `nproc`, `os.cpus()` and `runtime.NumCPU()` report
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
  - **More small instances fit per node.** A Free instance books a quarter of
    today's CPU and a Hobby instance half.
  - **Burst above the baseline is not guaranteed.** On a busy node an
    instance falls back toward its baseline, as `cpu.weight` already makes it
    do today.
  - **Scale apps see two CPUs** (four today), and their sustained CPU doubles.
  - **Mixed versions:** nothing new is stored or sent on the wire, so no
    version rejects another. An older vmmd does not run the check: a listed
    app it restores keeps the snapshot's count. Apps are listed only once
    every vmmd runs the check.
  - **Why the check is at restore, not in the snapshot row:** a recorded
    count would have to cross the vmmd capture responses and the
    admitted-capture request. Both are strict about unknown fields, so an
    older schedd or vmmd would reject them. Counting threads needs no new
    state and also covers adopted and natively captured VMs.
- **Rollout:**
  1. Benches: `TestMetalRequestCostBench`, and `FAAS_RESTORE_BENCH_VCPUS` on
     `TestMetalRestoreTimingBench` (this ADR's PR).
  2. vmmd's restore check (`pkg/fcvm/restore_vcpus.go`). It is inert while
     no app is listed: every snapshot was captured at the count its wakes ask
     for.
  3. schedd's `guest_vcpu_from_cpu_apps` / `FAAS_GUEST_VCPU_FROM_CPU_APPS`:
     boot specs and admission bookings of listed apps use the derived count.
  4. vmmd accepts a 2,000-millicore ceiling. It ships a release before
     schedd sends one, because an older vmmd rejects an unknown
     `cpu_millicores` value.
  5. schedd: listed apps book the baseline and get the ceiling (Scale: two
     cores, two vCPUs).
  6. Production A/B on one node: two identical apps each for Go and Node, one
     of each listed, plus one CPU-bound app and one Scale app. Run the
     300 → 600 req/s step test. Compare cgroup CPU per request, p50/p99
     latency, `gateway_wake_latency_seconds`, cold boot time and liveness.
  7. If the A/B confirms it (owner decision): set `*`, and change the plan
     pages, API, CLI, docs and spec §4.4 in the same release. The switch is
     removed a release later.
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
  - A proportional ceiling as well as baseline (Lambda's model: 250 m on
    Free, 500 m on Hobby). It would make Free and Hobby apps four and two
    times slower on CPU-bound work even on a quiet node.
  - One core for every plan, including Scale. A 2 GB instance would get the
    same ceiling as a 256 MB one.
- **Owner answers (2026-11-02):**
  1. Scale: two vCPUs with a two-core ceiling (decision 1), not four, and
     not one.
  2. Plan pages show RAM, the CPU baseline and burst ceiling, and instances
     per app; guest vCPUs are no longer a plan line (decision 5).
