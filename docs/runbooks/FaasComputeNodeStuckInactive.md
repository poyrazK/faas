# FaasComputeNodeStuckInactive

Source: no alert fires for this today — see [Detection gap](#detection-gap).
Severity: **page** (every app on the node returns 503).

## What this is

A compute node whose `compute_nodes.active` is `false` is invisible to
schedd's placement. If it is the only node, **every app on the platform
returns 503** and no wake can succeed.

The failure is sticky. Two independent mechanisms both decline to bring
the node back:

  1. **schedd's heartbeat never re-probes it.** `Heartbeat.Tick`
     enumerates `store.ActiveComputeNodes(ctx)`, whose query ends
     `where active = true` (`pkg/state/pgstore.go`). Once a node is
     marked inactive it is never listed, so it is never pinged and
     `last_heartbeat_at` freezes at the moment of death — making the row
     look progressively more dead the longer the condition lasts.
  2. **vmmd cannot reactivate itself.** `UpsertComputeNodeFromVmmd` sets
     `active = compute_nodes.active` on conflict — it deliberately
     preserves the flag so an operator-drained node cannot un-drain
     itself by restarting. Correct for drains; it also means a *crashed*
     node that restarts and re-registers stays out of rotation forever.

Note that `SetComputeNodeActive`'s doc comment claims "the heartbeat
goroutine uses it again to reactivate a drained row on the next
successful dial". No such caller exists — every
`SetComputeNodeActive(..., true)` call site is an operator/CLI path.
Do not rely on that sentence.

## Symptom

Customers see `503` with body `node unavailable`. schedd logs:

```
capacity_unavailable: placement: no active compute_node fits 1032 MB billable
(per-node ceilings: see compute_nodes.admission_ceiling_mb) / 4 vCPU
(per-node budgets: see compute_nodes.vcpu_budget) across 0 candidates
```

**`across 0 candidates` is the tell.** It means the node registry is
empty, not that the hardware is full. A genuine capacity problem shows a
non-zero candidate count.

## Verify

```bash
# Which nodes are active, and how stale is each heartbeat?
sudo -u postgres psql -d faas -c "
  select name, active, last_heartbeat_at, now() - last_heartbeat_at as age
    from compute_nodes order by last_heartbeat_at desc nulls last;"
```

A row with `active = f` and an `age` that is not advancing between two
runs of this query is stuck — the heartbeat is not re-probing it.

```bash
# Is the daemon actually alive on the box? Usually yes — it restarted.
systemctl is-active faas-vmmd
sudo journalctl -u faas-vmmd --since '-1h' --no-pager | grep 'compute_node registered'
```

A recent `compute_node registered` line with `active = f` still in the DB
is the exact signature of this runbook.

## Common cause: vmmd OOM

```bash
sudo journalctl -u faas-vmmd --since '-2h' --no-pager | grep -i 'OOM killer'
sudo dmesg -T | grep -i 'Memory cgroup out of memory'
```

vmmd runs in the shared `faas-cp.slice`. Before 2026-09-03 it carried no
`MemoryMax` at all — the only faas daemon without one — so it could
consume the whole slice and trigger a slice-level OOM. Observed once at
2.1 GB RSS against a 3 GB slice under sustained load. The unit now sets
`MemoryHigh=512M` / `MemoryMax=1G`, which contains the blast radius but
does **not** explain the growth; that remains undiagnosed. If you see
vmmd RSS climbing past a few hundred MB, capture a heap profile before
restarting it.

The OOM can land **minutes after** the load that caused it. A load test
that "finished cleanly" is not evidence the node is healthy.

## Mitigation

Restore the node with the minimal write — one column, leaving every
capacity value vmmd re-registered:

```sql
UPDATE compute_nodes SET active = true WHERE name = '<node-name>';
```

Service returns within ~15 s, and `last_heartbeat_at` starts advancing
again once the heartbeat re-enumerates the row.

Prefer this over the operator endpoint (`POST /v1/compute-nodes`, which
routes to `UpsertComputeNodeFromOperator` and does set `active = true`)
unless you are also correcting capacity: that path overwrites `vpcpus`,
`mem_mb`, `max_concurrency` and `admission_ceiling_mb` with whatever the
caller supplies, so a wrong value mis-sizes admission.

`gregalectl` also exposes an activate path
(`cmd/gregalectl/commands_compute_nodes.go`) backed by
`SetComputeNodeActive(ctx, id, true)`.

**Before reactivating, confirm the node was not drained on purpose.**
Nothing in the row distinguishes an operator drain from a watchdog
deactivation — that ambiguity is the root defect, and resolving it needs
the `compute_node_lifecycle` states (`active` / `draining` /
`unavailable` / `recovering`) from issue #1184 Workstream B / ADR-137.
Check with whoever owns the box first.

## Detection gap

There is no alert for this. `FaasDaemonDown` fires on
`up{job="vmmd"} == 0 for 2m`, which does **not** cover the common case:
vmmd is OOM-killed and systemd restarts it within seconds, so `up`
recovers while the node stays out of rotation. A 2026-09-03 incident went
undetected for ~20 minutes and was found only by manual inspection.

Closing this needs a gauge for the count of active compute nodes plus an
alert at zero (and, on a multi-node fleet, on any decrease). ADR-137's
recovery-timeline metrics are the natural home; until they land, the
`psql` check above is the only detection.

## Cross-references

  - `pkg/sched/heartbeat.go` — `Tick` / `probeNode`, the staleness gate.
  - `pkg/state/pgstore.go` — `ActiveComputeNodes` (`where active = true`),
    `UpsertComputeNodeFromVmmd`, `SetComputeNodeActive`,
    `MarkComputeNodeInactive`.
  - `pkg/sched/deadnode_reconciler.go` — the billing backstop for
    instances stranded on a dead node.
  - `deploy/ansible/roles/vmmd_service/files/faas-vmmd.service` — the
    memory bounds and why they are safe under `Delegate=yes`.
  - ADR-137 / issue #1184 Workstream B — the lifecycle-state redesign
    that makes recovery automatic.
