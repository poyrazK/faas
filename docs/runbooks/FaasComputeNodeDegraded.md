# Compute node degraded

`FaasComputeNodeDegraded` means a compute node answers its heartbeat but has
proven it cannot serve a wake (ADR-20261022184531). `FaasNoComputeNodeReady`
pages when no heartbeated node is ready at all.

## What the platform already did

- **Placement avoids the node.** Wakes go to ready nodes. A degraded node gets
  a wake only when no ready node has capacity.
- **Refused wakes are re-placed.** A wake the node refused before starting
  the VM moves to another node in the same gateway hold
  (`schedd_wake_failover_total`).
- **Running VMs keep running.** Degraded is not `lifecycle=unavailable`:
  nothing is migrated or parked.

## Why the node is degraded

```sql
SELECT name, lifecycle, readiness, readiness_reason,
       wake_breaker_open_until, wake_breaker_reason
  FROM compute_nodes ORDER BY name;
```

- **`readiness_reason` names a base generation.** The node's vmmd cannot adopt
  a configured runtime base generation. Neither its cache nor shared storage
  holds it: this is the 2026-10-22 fsn-3 shape, a node rolled back after
  another node published a newer generation. Confirm from the node:

  ```bash
  sudo /opt/faas/current/bin/vmmd base-adoption-check --serving-pid "$(pgrep -xo vmmd)"
  ```

  Exit 3 lists each `stale` base. Roll the node forward to the fleet's
  release, or re-run its rollout, whose pre-stage publishes the missing bases
  (ADR-20261024191249).
- **`wake_breaker_open_until` is set.** Some schedd saw
  `NodeWakeBreakerFailures` (3) consecutive platform failures within 60 s:
  - a base generation or scan sidecar refusal;
  - vmmd failing to acquire a jail lease or set up the network before the VM
    started.

  The breaker stays open for 30 s. After that, the next wake there probes the
  node: a success closes the breaker, and a platform failure reopens it. A
  breaker that keeps reopening is a real node fault. Read vmmd's log on the
  node:

  ```bash
  journalctl -u faas-vmmd --since -15min | grep -E 'network setup|acquire lease|scan_critical'
  ```

## When to drain

Drain only if the node keeps failing and you need its capacity serving
elsewhere (`gregalectl compute-nodes drain <node>`). A drain moves running
instances, which degraded alone never does.
