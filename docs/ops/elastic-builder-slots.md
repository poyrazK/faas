# Elastic builder slots

ADR-20261118154632. Each compute node runs a number of builds you set without
a restart. A node can run more builds than its one reserved slot by
borrowing memory its tenants are not using, and gives it back when tenants
need it.

## The two settings

| Key | Value | Default |
| --- | --- | --- |
| `builder_elastic_slots` | `off` or `on` | `off` |
| `builder_slots` | `{"default": {"reserved": 1, "max": 2}, "nodes": {"<node>": {"reserved": 1, "max": 8}}}` | `{"default": {"reserved": 1, "max": 2}}` |

- While `builder_elastic_slots` is `off`, builderd runs its fixed allocator and
  `builder_slots` is stored but not used.
- A node uses its own entry under `nodes` (its `FAAS_NODE_NAME`), else
  `default`.
- `reserved` slots run in `faas-cp-build.slice`, are funded out of tenant
  sizing (ADR-203) and never give a build back. A node runs the reserved slots
  it is provisioned with: the deploy manifest's `builder_reserved_slots`
  (default 1), which node_join turns into `FAAS_BUILDER_RESERVED_SLOTS` and
  the build slice, control-plane slice and tenant fence sized for that count
  (ADR amendment 1). `builder_slots`' `reserved` (1 to 4) cannot add or remove
  one; builderd logs a mismatch and runs its provisioned count. With the flag
  `off` the fixed allocator runs the same provisioned count as guaranteed
  slots.
- `max` counts every slot. The `max - reserved` slots above are **borrowed**:
  they run in `faas-borrowed.slice` and use memory the tenant slice is allowed
  but not using.
- A node runs at most its online CPUs ÷ 2 builds (each builder VM has 2 vCPU)
  and logs `builder_slots asks for more builds than this node's CPUs run;
  clamped` when it lowers `max`.
- Lowering `max` never cancels a running build; the node stops claiming until
  it is below the new value.

```bash
gregalectl config set --key builder_slots --value '{"default":{"reserved":1,"max":2},"nodes":{"gregale-compute-1":{"reserved":1,"max":3}}}' --reason "elastic slots canary" --yes
```

```bash
gregalectl config set --key builder_elastic_slots --value on --reason "elastic slots canary" --yes
```

## How tenants stay first

Every check reads the node's own `faas-tenant.slice` and
`faas-borrowed.slice` cgroups and `/proc/meminfo`, never the control plane.

1. **Admission (builderd).** A borrowed slot claims a build only when tenant +
   borrowed memory + 3,328 MiB is at most 70% of the tenant slice's
   `memory.max`, `MemAvailable` keeps 1 GiB after it, no give-back happened
   on the node in the last minute, and vmmd reports
   `supports_borrowed_builders`.
2. **Give-back (builderd, every 250 ms).** Above 85%, or below 1 GiB
   available, builderd cancels the newest borrowed build. It returns to the
   queue at its place and restarts; its build log says why.
3. **Forced give-back (vmmd).** Above 92%, or below 512 MiB available, for
   1 s, vmmd destroys the newest borrowed builder itself. It runs whether or
   not builderd is up, and whatever the flag says while a borrowed builder
   exists.
4. **Kernel order.** Borrowed builders run with `oom_score_adj` 1000, so a
   host out of memory kills a borrowed build before any tenant VM or daemon.

A build given back twice runs only on a reserved slot. Tenant admission
(schedd) does not change.

## Before turning it on

- Release a version that has the four PRs on every compute node: vmmd
  advertises borrowed builders only with `faas-borrowed.slice` present
  (ansible `systemd_slices`), and builderd asks only when vmmd does.
- Canary: turn the flag on and give one node `max: 3` while every other node
  keeps the default. Watch for at least 48 h:
  - tenant OOM kills stay at 0 (`node_vmstat_oom_kill`, `FaasHostOOMKill`);
  - wake latency p95 does not move;
  - `builderd_build_preemptions_total` and `FaasBuildPreemptionsHigh`;
  - build queue wait (`builderd_build_queue_wait_seconds`).

## Metrics

| Metric | Meaning |
| --- | --- |
| `builderd_build_slots_limit` | Applied `max` (2 with the flag off) |
| `builderd_build_slots_reserved` | Applied `reserved` |
| `builderd_build_slots_active{kind}` | Builds holding a reserved or borrowed slot |
| `builderd_build_preemptions_total{actor}` | Builds given back, by builderd or vmmd |
| `vmmd_builder_preemptions_total` | Borrowed builders vmmd destroyed |
| `vmmd_builder_borrowed_memory_bytes` | `faas-borrowed.slice` memory |

Runbook: [FaasBuildPreemptionsHigh](../runbooks/FaasBuildPreemptionsHigh.md).
