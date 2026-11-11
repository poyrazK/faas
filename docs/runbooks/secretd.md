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

Until slice 3c, no compute node calls it.

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
