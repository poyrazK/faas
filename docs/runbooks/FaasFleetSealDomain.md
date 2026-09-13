# FaasFleetSealDomain

## Symptom

This alert means a node is running schedd without a successful fleet sealed-secret verification, or verified nodes disagree on the cluster signing `kid`. Keep the affected node drained. Do not copy another node's `host.age`: that identity remains host-local.

## Check

Inspect the node-exporter proof and the identities:

```bash
cat /var/lib/node_exporter/textfile_collector/faas_fleet_seal.prom
sudo gregalectl host-age status --json
sudo gregalectl fleet-seal verify \
  --fleet-key /etc/faas/secrets/fleet.age \
  --host-key /etc/faas/secrets/host.age \
  --db-env /etc/faas/compute-db.env --json
```

`ready=true`, `probe_ok=true`, and `jwt_round_trip_ok=true` must all be present. `fleet_recipient` must agree across nodes, `host_recipient` must remain different on each node, and every node must report the singleton cluster `kid`.

## Recover

For an existing cluster that still has host-sealed ciphertext, run the migration on a trusted host whose legacy `host.age` can open the current material. The command opens each row only in memory and uses compare-and-swap writes so concurrent customer secret rotations are not overwritten:

```bash
sudo gregalectl fleet-seal migrate \
  --fleet-key /etc/faas/secrets/fleet.age \
  --legacy-host-dir /etc/faas/secrets \
  --db-env /etc/faas/compute-db.env --json
```

Repeat `fleet-seal verify` on every node. A failed `deploy join-node` leaves its `compute_nodes` row drained and stops before service activation. After all nodes show the same recipient and `kid`, activate the node through the normal join controller. If migration reports a compare-and-swap conflict, a customer or key rotation won the race; rerun migration rather than editing ciphertext directly.
