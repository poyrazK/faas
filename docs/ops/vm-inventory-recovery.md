# VM inventory recovery qualification

The signed process inventory repairs a service VM that disappears on a healthy
node. A complete empty inventory covers a node whose only VM is gone. Missing
resource metrics never assert VM absence. See [ADR-419](../adr/419-authoritative-vm-inventory-recovery.md).

## Enablement

Ship vmmd and schedd from a compatible release before enabling repair. New vmmd
signs inventory with its registered node key; older receivers ignore the new
field and preserve the original capacity signature contract. Old producers,
missing keys and invalid signatures cannot trigger inventory repair.

Leave `faas_node_inventory_reconcile_enforce: false` while checking reports.
Run the portable regression suite:

```sh
go test -race -count=1 ./pkg/sched ./pkg/scheddgrpc ./pkg/fcvm ./cmd/vmmd \
  -run 'Test(NodeInventory|NodePresence|InstanceInventory|ReportCapacity|Capacity)'
```

On an isolated native x86_64 Linux KVM acceptance host with the normal metal
kernel/base/layer assets, run:

```sh
sudo make test-metal PKGS=./pkg/fcvm RUN_REGEX='^TestMetalInstanceInventoryAfterProcessKill$'
sudo make leakcheck
```

The portable service test asserts autonomous replacement through the real
scheduler with a fake VMM. The native test asserts real process inventory and
cleanup. Together they cover the two boundaries; they are not a claim that a
live fleet recovery drill has been executed.

Enable `faas_node_inventory_reconcile_enforce: true` on one canary scheduler
through its normal Ansible role. Keep the older
`FAAS_SCHEDD_RECONCILE_ENFORCE` switch unset: that sweep uses optional metrics.
The new roles render the explicit `FAAS_SCHEDD_NODE_INVENTORY_ENFORCE` switch
for both control-plane and compute schedulers.

## Fleet acceptance

On an isolated fleet, deploy a one-replica service, wait beyond startup grace,
and record its instance ID and Firecracker PID. Kill only that Firecracker
process. Keep vmmd/schedd and the node running. For the lost-relay scenario,
disable the direct exit notification in the acceptance fixture; the inventory
must independently drive recovery. Do not send customer traffic until a new
RUNNING replica appears. Then probe the public route and verify:

- Exactly the desired number of healthy replicas returns automatically.
- The old row is terminal with a timestamp, stops contributing running usage,
  and no longer holds admission or host resources.
- The public route adopts the replacement; no duplicate replacement appears.
- Recovery fits the measured report-confirmation + cleanup + boot/readiness
  budget. Record the timings and the deployed release, rather than claiming a
  fixed recovery deadline from a unit test.
- A scheduler restart or interrupted inventory stream requires fresh evidence;
  neither causes healthy VMs to be destroyed.

Compare `schedd_instance_divergence_total{outcome="suppressed"|"failed"}` with
host reality and replica/request health. Restore the host variable to false to
stop inventory repairs. A database/cleanup error retries with fresh inventory;
a silent node remains the node-failure controller's responsibility.
