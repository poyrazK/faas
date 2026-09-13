# FaasComputeNodeTrustedKeyOverflow

Each active compute node may authenticate capacity reports with one current
ECDSA key and, for 24 hours after an explicit rotation, one previous key. The
schedd registry binds each key to its owning `compute_nodes.id`; inactive,
revoked, expired, and cross-node keys are excluded.

## Symptom

`FaasComputeNodeTrustedKeyOverflow` fires when schedd observes more than two
trusted capacity keys for one node. This indicates database lifecycle drift or
an incomplete key rotation.

## Check

Identify the node from the alert label and inspect credential metadata only:

```sql
select compute_node_id, key_id, key_state, created_at, valid_until, revoked_at
from compute_node_keys
where compute_node_id = '<node-id>'
order by created_at desc;
```

Confirm the node is active and that its on-disk key matches the sole current
row:

```sh
sudo gregalectl node-key status
systemctl status faas-vmmd
```

## Recover

Drain the affected node before changing credentials. Keep the row whose
`key_id` matches `gregalectl node-key status` as `current`. Retain at most the
immediately previous key as `overlap` with a future `valid_until`; revoke and
remove older rows. Restart schedd or wait for `compute_node_changed` to refresh
the registry, then reactivate the node only after signed capacity reports are
accepted.

Verify recovery:

```promql
schedd_compute_node_trusted_keys{node_id="<node-id>"}
increase(schedd_capacity_signature_rejected_total[10m])
```

The trusted-key gauge must be one during steady state or two during an approved
rotation, and signature rejections must stop increasing.
