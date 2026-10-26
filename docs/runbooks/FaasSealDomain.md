# FaasSealDomain

Seal domains ([ADR 20261022111812](../adr/20261022111812-seal-domains-per-reader.md))
give each reading service its own age identity in
`/etc/faas/secrets/domains/<domain>.age`. Two alerts cover them. Neither
fires before an operator stages a domain.

## Symptom

- **FaasSealDomainUnverified:** `gregalectl seal-domain verify` failed on a
  host. The `seal_domains` role runs `verify` after it stages the identity,
  and writes `faas_seal_domain_ready{domain}` to the node's textfile
  collector. 0 means one of these:
  - the identity file is missing;
  - it does not open the domain probe (a different key from the one
    writers seal to);
  - the database was unreachable.
- **FaasSealDomainIdentityMissing:** a reader daemon (outboundd for
  `outbound`, vmmd for `runtime`, githubd for `source`, imaged for `build`,
  gatewayd-internal for `edge`, meterd for `alerting`, s3-gatewayd for
  `storage`, schedd for `control`) started without the domain identity while other readers of the same
  domain hold it. This is usually a node that joined or was rebuilt after
  the rollout, before the domain's `SEAL_DOMAIN_*` secrets were in CD, or a
  vmmd that has not restarted since its key was staged.

Impact depends on the write mode:
- **Before writers seal to the domain alone** (apid's
  `FAAS_SEAL_DOMAIN_<DOMAIN>_WRITES` is not `domain`): nothing is broken
  yet, but the host blocks switching to `domain`.
- **After the switch:** the reader on that host cannot open the domain's
  secrets. For `runtime`, every wake on the node fails for an app with a
  domain-sealed secret, which is every app with a secret once
  `seal-domain migrate --domain runtime` has run. For `outbound`, that
  host's outboundd cannot use customer AI provider credentials. For
  `source`, that host's githubd cannot verify GitHub webhooks or fetch
  source. For `build`, that host's imaged cannot pull from customers'
  private registries.

## Check

On the host the alert names:

```bash
sudo ls -l /etc/faas/secrets/domains/
cat /var/lib/node_exporter/textfile_collector/faas_seal_domain_<domain>.prom
sudo gregalectl seal-domain verify --domain <domain> --db-env /etc/faas/compute-db.env
```

- The identity must be `<domain>.age`, mode 0400, owned by root.
- `verify` must print `ready` with the same recipient as on the hosts that
  work. A different recipient means this host has another environment's
  key or a regenerated one; do not re-publish the probe from it.

For `runtime`, also check what vmmd loaded at start:

```bash
journalctl -u faas-vmmd -b | grep 'vmmd: unseal identities loaded'
```

`"runtime_domain":true` means vmmd loaded the identity. vmmd reads it only
at start, so a key staged after vmmd started shows `false`, and
`faas_seal_domain_loaded{domain="runtime"}` stays 0, until the next restart.

To see the write mode, look at apid on the control plane:
`journalctl -u faas-apid -b | grep '"runtime seal domain"\|"outbound seal domain"'`
shows `write_mode` for each domain.

## Recover

Stage the identity through CD when the domain's `SEAL_DOMAIN_*` secrets are
set: rerun the control-plane CD, or the node's join. Otherwise stage it from
the operator machine with the local variables file used for the rollout. See
[Seal domains rollout](seal-domains-rollout.md#where-the-keys-and-settings-live).
The `seal_domains` role installs the identity and reruns `verify`, which
clears FaasSealDomainUnverified.

1. **Control-plane host (`outbound`, `source`):** converge the role alone.
   It restarts outboundd and githubd itself.

   ```bash
   gregalectl deploy converge-control-plane --roles seal_domains --manifest-file <signed manifest> --ssh-known-hosts-file <known_hosts> --ansible-vars-file <local vars file>
   ```

2. **Compute host (`runtime`):** drain the node, converge it, and restart
   vmmd, which loads the identity only at start.

   ```bash
   gregalectl compute-nodes drain --node <fqdn> --reason 'stage runtime seal-domain identity'
   gregalectl compute-nodes drain-status --node <fqdn>
   ANSIBLE_LIMIT=<host> make ANSIBLE_INVENTORY=<inventory> bootstrap-compute
   ssh <host> 'sudo systemctl restart faas-vmmd'
   gregalectl compute-nodes activate --node <fqdn> --reason 'runtime seal-domain identity staged'
   ```

   - Repeat `drain-status` until it exits 0 before you converge.
   - The bootstrap needs the seal-domain variables from the rollout in its
     inventory variables.
   - Activate only once the node reports
     `faas_seal_domain_loaded{domain="runtime"} 1`.
3. **The key itself is lost** on every host, with no backup: no identity
   opens the domain's secrets. Treat this as a secret-store incident
   ([Customer secret store compromise](customer-secret-store-compromise.md)).
   Customers must set the affected secrets again. Never run
   `seal-domain init --force` to "fix" a lost key: it creates a new key that
   opens nothing already stored.

Keep a drained node out of service until both alerts clear for it.
