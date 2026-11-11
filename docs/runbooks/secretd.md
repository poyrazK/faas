# secretd: the control plane's secret opener

ADR-20261110210000 §3. `secretd` opens customer runtime secrets for the
compute node that runs the app (the owner, or a peer the owner granted), so
compute nodes can stop holding the runtime seal domain's identity.

## Turn it on (control plane)

1. In the GitHub environment (`production`, `production-us`), set the
   variable `SEAL_DOMAIN_RUNTIME_ON_CONTROL_PLANE` to `true`. The next
   control-plane converge stages `/etc/faas/secrets/domains/runtime.age`
   (0400 root) on the control plane. With `SEAL_DOMAIN_KEY_SOURCE=escrow`,
   CD then fetches the runtime identity from the escrow for the control
   plane too.
2. Set `faas_secretd_listen: "tcp://<control plane private address>:8098"` in
   the control plane's Ansible variables and converge. The role creates the
   `faas-secretd` user, issues the `secretd.faas` leaf and installs the unit
   and its drop-in. The release activation starts it.
3. Check: `systemctl status faas-secretd`, and
   `journalctl -u faas-secretd` shows `secretd: serving` with
   `identities=1` (2 during a rotation).

## Turn it on for a compute node (slice 3c-1)

Prerequisite: `faas_seal_domain_runtime_binding_required: true` on compute
(every stored runtime envelope is bound; secretd refuses unbound ones).

A peer can open an app's secrets only if the app's owner granted it, so
grants go on everywhere before any vmmd asks secretd:

1. **Grants (every compute node).** Set
   `faas_secretd_target: "tcp://<control plane private address>:8098"` for
   all compute nodes and converge. vmmd_service issues each node's
   `secret-opener` client leaf on the control plane; compute_only_service
   gives schedd `FAAS_SCHEDD_SECRETD_TARGET`. Nothing opens differently yet.
   Check: each schedd's journal shows `schedd: peers granted app secrets
   through secretd`; `secret_grants` gains rows as apps run on peers, and
   `sched: secret grant refused or failed` stays absent.
2. **Opening (one node at a time).** Set `faas_vmmd_secretd_open: true` for
   one node and converge it (`--limit`). vmmd restarts with
   `FAAS_RUNTIME_SECRET_OPENER=secretd`. Check: vmmd's journal shows `vmmd:
   app secrets opened by secretd`, wakes there add `secret_open_audit` rows
   (`via = owner`, `grant` for peer placements), and no wake fails with
   PermissionDenied. Then the next node.

Back out a node by setting `faas_vmmd_secretd_open: false` and converging:
vmmd opens with its own runtime identity again (it still holds it until
slice 3d).

## Rotation

The runtime domain rotates as before (`docs/runbooks/seal-domain-key-escrow.md`).
While the previous identity is staged, the drop-in hands it to secretd as
`faas_seal_domain_runtime_previous`. The converge that stages or removes it
restarts secretd.

## Audit

Every open is a row in `secret_open_audit` (node, app, `owner` or `grant`,
entry count, time), written before any plaintext leaves. Daemons other than
secretd cannot write or delete it.

```sql
SELECT n.name, a.slug, o.via, o.entries, o.opened_at
  FROM secret_open_audit o
  JOIN compute_nodes n ON n.id = o.node_id
  JOIN apps a ON a.id = o.app_id
 WHERE o.opened_at > now() - interval '1 hour'
 ORDER BY o.opened_at DESC;
```

A node that opens more than `FAAS_SECRETD_APPS_PER_NODE_HOUR` (default 1000)
distinct apps in an hour is refused, and secretd logs
`secretd: node over its distinct-app budget`. Treat it as a compromised node:
drain it and rotate the runtime domain.

## Turn it off

Set `faas_secretd_listen: ""` and converge: the role removes the drop-in and
stops the unit. Then unset `SEAL_DOMAIN_RUNTIME_ON_CONTROL_PLANE`: the next
converge removes `runtime.age` from the control plane. Do not do this once
compute nodes have stopped holding the runtime identity.
