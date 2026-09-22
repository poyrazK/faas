# ADR-204 · Sustained-CPU admission uses the spec's overcommit factor

- **Status:** accepted
- **Date:** 2026-09-22
- **Decision:** The per-node sustained-CPU admission budget becomes
  `vpcpus * 1000 * api.CPUOvercommit` instead of `vpcpus * 1000`, and all four
  places that computed it independently now call one helper
  (`pkg/sched.cpuBudgetMillicores`). `compute_nodes.vpcpus` is fixed to mean
  the **physical** CPU count everywhere, because the budget multiplies it.
- **Why:** `ChoosePlacement` gated the same resource twice with two different
  overcommit factors, and the stricter one silently won.

  The guest-vCPU gate checks `compute_nodes.vcpu_budget`, which
  `api.DeriveNodeSizing` sets to `hostCPUs * CPUOvercommit` — spec §1's 8×.
  The sustained-CPU gate added in #3165 then re-checked the same hardware at
  `vpcpus * 1000`, i.e. 1×. On a 4-core production node advertising
  `vcpu_budget=32`, that admitted exactly **4** instances at the default
  `DefaultAppCPUMillicores = 1000`, making the overcommit-aware budget dead
  code.

  An app's `CPUMillicores` is a cgroup v2 `cpu.max` **quota** — a ceiling on
  burst, not a reservation. Summing quotas against raw physical cores
  reserves peak capacity for every idle instance, which is the opposite of
  what a scale-to-zero platform is for. Spec §1 chose 8× precisely because
  parked and idle instances dominate.

  The observed consequence: fsn-2 was already running 8 instances (8,000
  millicores) against a 4,000 "budget" because they were placed before #3165
  deployed, and fsn-3 sat at exactly 4 instances = 4,000 = saturated. schedd
  then logged `capacity_unavailable` every minute and could not re-satisfy
  the pinned `min_instances` floors of `imgpipe-strip` (8) and
  `imgpipe-gateway` (4). No node could drain, so no rolling compute rollout
  could complete.

- **Why CPU and RAM are treated differently.** Oversubscribing CPU degrades
  latency under simultaneous load and the kernel throttles each instance to
  its own `cpu.max`. Oversubscribing RAM OOM-kills. The RAM admission ceiling
  therefore stays un-overcommitted (see ADR-203), and only CPU carries the
  factor. This asymmetry is deliberate, not an oversight.

- **The `vpcpus` ambiguity this also closes.** Two paths disagreed about what
  the column means:
  - `node_join.yml` set `FAAS_COMPUTE_VCPUS={{ ansible_processor_vcpus }}` —
    physical, 4 on the production nodes.
  - `cmd/vmmd/config.go` defaulted it to `sizing.VCPUSlots` — already
    overcommitted, 32 on the same box.

  With the budget now multiplying by `CPUOvercommit`, the vmmd default would
  have applied the factor twice and produced a 64× budget. `NodeSizing` gains
  `HostCPUs` and vmmd advertises that, so `vpcpus` is unambiguously physical.
  It still falls back to the legacy constant when the host probe fails, so a
  node never registers `vpcpus=0` and trips migration 00123's CHECK.

- **Consequences.**
  - A 4-core node's sustained-CPU budget goes 4,000 → 32,000 millicores,
    which funds 32 instances at the default per-app quota — the same count
    its `vcpu_budget` already advertised.
  - #3165's intent is preserved: a node genuinely out of CPU headroom is
    still skipped, and placement still prefers the node with the most
    headroom over sticky warm affinity. Only the factor changed.
  - The two tests that pinned the 1× figure now derive their saturation
    point from `cpuBudgetMillicores`, so they track the formula instead of
    restating a literal that can drift from it again.

- **Alternatives rejected.** Raising `DefaultAppCPUMillicores`: it is a
  customer-visible quota with a closed valid set (250/500/1000) and is not
  the broken part. Deleting the sustained-CPU gate: physical headroom is a
  real signal and #3165 added it for a reason; it just needs the spec's
  factor. Making `vcpu_budget` 1×: that would contradict spec §1 and shrink
  every node's capacity eightfold.
