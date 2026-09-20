# Instance divergence

`FaasInstanceDivergence` means schedd believes instances are live that the
owning vmmd is not reporting (ADR-191). Each one is billing the customer, is
holding an admission slot against the node's RAM ceiling, and may still be
receiving routed requests that fail.

## What the platform already did

**In the default configuration, nothing.** The sweep ships report-only:
`FAAS_SCHEDD_RECONCILE_ENFORCE` must be `1` before any row is written. Check
which mode the node is in before assuming the rows were repaired:

```bash
systemctl show faas-schedd -p Environment | tr ' ' '\n' | grep RECONCILE
```

With enforcement on, each confirmed row was transitioned to `failed` and its
admission slot released. `failed` is cold-bootable (ADR-005), so the customer's
next request still serves; it pays the cold-boot path instead of a restore.

## Why a row has to be very clearly dead before it counts

The sweep will not act unless all four hold:

1. the node reported at least one of its own instances on the same snapshot;
2. the instance is older than `InstanceDivergenceGraceSeconds` (60 s);
3. it was absent on two consecutive 30 s sweeps;
4. the snapshot was not empty.

So a firing alert is not a flaky-telemetry signal. Something removed a VM
without moving its row.

## Triage

1. Find the instances and the node:

   ```promql
   increase(schedd_instance_divergence_total[15m]) > 0
   ```

   The instance, app and node ids are in schedd's log line
   `sched: instance divergence detected` (report-only) or
   `sched: instance divergence repaired` (enforcing).

2. Confirm from the node's side. On the compute node:

   ```bash
   sudo ls /srv/fc/jail/*/ | head
   sudo systemctl status faas-vmmd
   ```

3. Match the cause to the evidence:
   - **vmmd restarted.** `systemctl show faas-vmmd -p NRestarts`. Its boot-time
     `ReapOrphanedJails` tears down VMs with no live row, but rows whose VM died
     with the old process are exactly what this sweep finds. Expect a burst at
     the restart timestamp and nothing after.
   - **Host OOM killed Firecracker.** `journalctl -k | grep -i 'killed process'`.
     The liveness path usually catches this; a burst here means it did not.
   - **A destroy that failed after the VM died.** Look for
     `vmmd_ops_total{op="Destroy",code="err"}` around the same minute.
   - **A whole node dying.** Should not reach this alert — a silent node is the
     dead-node reconciler's job and this sweep skips it. If you see both alerts
     for one node, that is worth a bug report against ADR-191.

4. In report-only mode, repair by hand once you know the cause is real. There is
   no operator command for this yet; use the break-glass path in
   `docs/break-glass/database-repair.md` and record why.

## False positives

- **A telemetry outage long enough to span two sweeps** while the node keeps
  reporting some instances. Check `schedd_instance_stats_partial_errors_total`
  for the same node and window; a non-zero rate there means the reported set was
  incomplete, not that the VMs were gone.
- **A wake burst at exactly the grace boundary.** Instances admitted 60-90 s ago
  on a node under heavy load can miss two batches. Correlate with
  `gateway_wake_latency_seconds` and the node's CPU.

## Related

- `docs/adr/191-scheduler-divergence-and-bounded-dispatch.md`
- `FaasDaemonLoopStalled` — the other half of ADR-190/191, for a schedd that is
  up but not advancing.
- The dead-node reconciler handles the disjoint case: node silent rather than
  node reporting without the VM.
