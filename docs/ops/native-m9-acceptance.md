# Native x86 M9 acceptance

The M9 failure-safe acceptance runs on two native x86 compute nodes where each
node owns one schedd and one vmmd. The control-plane checkout executes the
test, and the owning schedd reconciles the stopped vmmd. It does not depend on
Lima or nested virtualization.

The gate is intentionally opt-in. Run it only from the designated acceptance
control-plane checkout, with the pair drained of customer workloads:

```sh
sudo bash -lc '
  cd /srv/gregale
  export DATABASE_URL="postgres:///faas?host=/run/postgresql&user=faas"
  export FAAS_TWO_NODE_NODE_A=fsn-2.faas
  export FAAS_TWO_NODE_NODE_B=fsn-3.faas
  export FAAS_TWO_NODE_SSH_A=faas-fsn-2
  export FAAS_TWO_NODE_SSH_B=faas-fsn-3
  export FAAS_TWO_NODE_ADDR_A=10.0.0.12
  export FAAS_TWO_NODE_ADDR_B=10.0.0.13
  export FAAS_M9_CONFIRM=native-x86
  make native-m9-acceptance
'
```

Use the addresses and SSH targets from the active Ansible inventory; the
values above are examples. The script requires `/etc/faas/m9-acceptance-host`
on the control-plane host so an ordinary production node cannot run the
fault drill accidentally.

The test stops node B's vmmd, waits for node B's owning schedd to mark it
unavailable, and verifies the `node.failed` event. A trap restarts vmmd when
the command exits. The
pre/post `leakcheck` runs are part of the gate.

The split-box manifest installs a node-local schedd on every compute host, so
the service preflight can run against two x86 compute nodes from the control
plane. The remaining live-migration and partition drills still require
workload fixtures and are tracked separately. This target is the native
replacement for the old two-node Lima heartbeat gate.
