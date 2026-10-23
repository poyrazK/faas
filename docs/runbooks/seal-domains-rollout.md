# Seal domains rollout

Turns on what [ADR 20261022111812](../adr/20261022111812-seal-domains-per-reader.md)
(seal domains, first slice) and
[ADR 20261022140000](../adr/20261022140000-break-glass-controls.md)
(break-glass controls) shipped:

1. seal the existing GitHub webhook secrets;
2. move customer outbound credentials to the `outbound` seal domain;
3. rehearse the break-glass release pause and credential revocation;
4. move customer runtime secrets (env vars and secrets) to the `runtime` seal
   domain.

Do each stage in staging first, then production. Stages 1 and 3 stand alone.
The steps of stages 2 and 4 run in order, and each has its own rollback.
Record in the change ticket who ran each step, when, and each domain's
recipient (recipients are public).

[FaasSealDomain](FaasSealDomain.md) covers the two alerts that watch a
rollout: a host whose `verify` failed, and a reader that started without the
domain identity.

## Before you start

- The environment runs a control-plane release with both ADRs. To check, on
  the control-plane host, `gregalectl seal-domain` and
  `gregalectl break-glass` must print their usage.
- Fleet sealing is healthy on every node: see
  [FaasFleetSealDomain](FaasFleetSealDomain.md#check). `fleet-seal verify`
  must report `ready=true`.
- Run every `gregalectl` command below on the control-plane host, with the
  `gregalectl` of the release deployed there. `seal-domain migrate` applies
  pending migrations before it re-seals. A newer binary would apply them
  before their data release.

## 1. Seal the GitHub webhook secrets

apid already writes a sealed copy whenever a webhook secret is set. This
stage backfills the copy for older rows. The copy is sealed to the fleet
recipient, which githubd already holds as `fleet.age`. The plaintext column
stays for the previous release.

```bash
sudo gregalectl seal-domain migrate --domain source --db-env /etc/faas/compute-db.env
```

Expect `0 failed`. A second run must report `0 scanned`. If any count shows
`changed concurrently`, an admin changed that secret during the run; run it
again.

Check: push to a repository connected in that environment and confirm the
build starts. githubd logs
`githubd: sealed webhook secret but no identity loaded` only if its unit lost
`fleet.age`.

Rollback: none needed. The previous release reads the plaintext column and
ignores the copy.

## 2. Outbound seal domain

outboundd is the only reader of customer outbound credentials, such as AI
provider keys. At the end of this stage, only `outbound.age` opens them, and
only outboundd holds it. apid holds only the public recipient.

### 2a. Create the identity (operator machine)

```bash
gregalectl seal-domain init --domain outbound --dir ~/gregale-seal/<environment>
```

This writes `outbound.age` and `outbound.age.pub`. Store `outbound.age` in the
same offline store as the environment's fleet identity before going on. Once
step 2d has run, a lost `outbound.age` means every customer has to enter
their outbound credentials again. There is no reverse migration. Staging and
production each get their own identity.

### 2b. Stage the reader; seal to both keys

Decode the environment's control-plane variables (`CP_ANSIBLE_VARS_B64`) into
a local file with mode 0600, and add:

```yaml
faas_seal_domains_managed: true
faas_seal_domain_outbound_identity_src: /Users/<you>/gregale-seal/<environment>/outbound.age
faas_seal_domain_outbound_recipient_src: /Users/<you>/gregale-seal/<environment>/outbound.age.pub
```

Keep these out of `CP_ANSIBLE_VARS_B64`. The CD runner does not have the
files, so the role would fail every control-plane release. While
`faas_seal_domains_managed` is unset, CD's converge skips the role and leaves
the staged files and drop-ins in place.

```bash
gregalectl deploy converge-control-plane --roles seal_domains --manifest-file <signed manifest> --ssh-known-hosts-file <known_hosts> --ansible-vars-file <local vars file>
```

The role does these steps in order:

1. Installs `/etc/faas/secrets/domains/outbound.age` (mode 0400) and its
   `.pub` file.
2. Restarts outboundd with the identity.
3. Runs `seal-domain verify --publish`, which writes the domain probe on the
   first host.
4. Only after that, restarts apid with the recipient.

With `faas_seal_domain_outbound_writes` left empty, apid seals each new
credential to both the domain and the fleet key. The current and previous
outboundd can both open it.

If more than one host runs outboundd, converge each one before step 2d.

Check, on each host:

```bash
cat /var/lib/node_exporter/textfile_collector/faas_seal_domain_outbound.prom
sudo gregalectl seal-domain verify --domain outbound --db-env /etc/faas/compute-db.env
journalctl -u faas-outboundd -b | grep 'outbound customer credential identities loaded'
journalctl -u faas-apid -b | grep '"outbound seal domain"'
```

- The `.prom` file shows `faas_seal_domain_ready{domain="outbound"} 1`.
- `verify` prints `ready`, with the same recipient on every host as in
  `outbound.age.pub`.
- outboundd logs `"domain_identity":true`.
- apid logs `"write_mode":""`, `"domain_recipient_staged":true` and
  `"recipients":2`.

Then save a new outbound credential on a test account, and make one call
through it and one through an older credential.
`journalctl -u faas-outboundd | grep 'outbound credential cannot be opened'`
must stay empty.

Rollback: set `faas_seal_domain_outbound_writes: fleet` and converge the role
again. apid goes back to sealing to the fleet key alone. Credentials saved
in the meantime also carry the fleet key, so rolling the release back is
safe at this stage.

To remove the stage completely:

1. Delete the apid drop-in
   (`/etc/systemd/system/faas-apid.service.d/60-faas-seal-domain-outbound.conf`)
   and the outboundd one (same file name under `faas-outboundd.service.d`).
2. Run `systemctl daemon-reload` and restart both units.
3. Only then delete the files in `/etc/faas/secrets/domains/`.

The drop-ins name the files as required credentials. A unit whose file is
missing does not start.

### 2c. Wait out the rollback window

A credential sealed only to the domain does not open in an outboundd that
lacks `outbound.age`. Every release before ADR 20261022111812 lacks it. Go
on only when both of these hold:

- every outboundd host passed the checks in 2b;
- outboundd's previous release (N-1) also carries the ADR, so rolling back
  one release keeps the reader.

### 2d. Seal to the domain only; re-seal stored credentials

Add to the local variables file, and converge the role again:

```yaml
faas_seal_domain_outbound_readers_verified: true
faas_seal_domain_outbound_writes: domain
```

apid then logs `"write_mode":"domain"` and `"recipients":1`. Re-seal the stored
credentials:

```bash
sudo gregalectl seal-domain migrate --domain outbound --readers-verified --db-env /etc/faas/compute-db.env
```

- Expect `0 failed`.
- If `changed concurrently` is not 0, a customer changed that credential
  during the run; run the migration again.
- When a second run shows only `current`, the migration is complete.
- If `failed` is not 0, no key on the host opens those envelopes. Those
  credentials were already broken for their customers; investigate with
  [FaasFleetSealDomain](FaasFleetSealDomain.md#recover).

Re-sealing a credential bumps the binding promotion revision of every app
bound to it. Pending promotion approvals for those apps must be renewed, so
run this step at a quiet hour. Repeat the call checks from 2b.

Rollback: set `faas_seal_domain_outbound_writes` back to empty and converge.
New credentials go back to carrying the fleet key too. Credentials re-sealed
in this step stay domain-only. From now on:

- Never remove `outbound.age` from outboundd.
- Never roll the control plane back past the first release that carried the
  ADR.
- A new or rebuilt control-plane host must run the `seal_domains` role,
  using the local variables file, before its outboundd serves traffic. CD
  does not stage the domain identity yet.

## 3. Break-glass rehearsal

These are the steps of
[Customer secret store compromise](customer-secret-store-compromise.md#1-contain-first-30-minutes),
run in staging:

```bash
sudo gregalectl break-glass status --db-env /etc/faas/compute-db.env
sudo gregalectl break-glass pause --db-env /etc/faas/compute-db.env --reason "break-glass rehearsal <ticket>"
```

Within seconds, builderd logs `builderd: break-glass release pause changed`
with `"paused":true`. While paused, on a staging test app:

- `gregale deploy` fails with 503 `platform_release_paused`;
- a build already queued stays queued;
- `gregale rollback` to a retained release works;
- changing an env var works.

Count what revocation would revoke. Without `--yes`, nothing is revoked:

```bash
sudo gregalectl break-glass revoke-credentials --db-env /etc/faas/compute-db.env
```

Never pass `--yes` in a rehearsal. It signs out every user and revokes every
API key and access token in the environment.

```bash
sudo gregalectl break-glass resume --db-env /etc/faas/compute-db.env --reason "break-glass rehearsal <ticket> done"
sudo gregalectl break-glass status --db-env /etc/faas/compute-db.env
```

`status` shows `release pause: false`, the queued build starts, and a deploy
succeeds. The audit log has two `break_glass.release_pause_set` events.

In production, run only `status` and the revoke count. They show that the
commands reach the production database without blocking any customer.

## 4. Runtime seal domain

vmmd is the only reader of customer runtime secrets: app and project
environment secrets, release bundles and snapshots, binding values and
sidecar env. At the end of this stage only `runtime.age` opens them, and
only vmmd on compute hosts holds it. apid holds only the public recipient.

The runtime domain has no dual mode. apid seals to the fleet key until the
switch in 4d, then to the domain alone.

### 4a. Create the identity (operator machine)

```bash
gregalectl seal-domain init --domain runtime --dir ~/gregale-seal/<environment>
```

Store `runtime.age` in the same offline store as the fleet identity before
going on. Once 4d has run, a lost `runtime.age` loses every customer env
secret: customers must set them all again. Staging and production each get
their own identity.

### 4b. Stage the readers

Add to the local variables file from 2b:

```yaml
faas_seal_domain_runtime_identity_src: /Users/<you>/gregale-seal/<environment>/runtime.age
faas_seal_domain_runtime_recipient_src: /Users/<you>/gregale-seal/<environment>/runtime.age.pub
```

As in 2b, keep these out of the variables CD uses (`CP_ANSIBLE_VARS_B64` and
the compute join artifacts).

1. **Control plane:** converge the role alone. apid gets the recipient and
   keeps sealing to the fleet key.

   ```bash
   gregalectl deploy converge-control-plane --roles seal_domains --manifest-file <signed manifest> --ssh-known-hosts-file <known_hosts> --ansible-vars-file <local vars file>
   ```

2. **Each compute host, one at a time:** the role stages `runtime.age` and
   verifies the probe (publishing it on the first host). vmmd loads the
   identity only at start, so drain the node and restart vmmd:

   ```bash
   gregalectl compute-nodes drain --node <fqdn> --reason 'stage runtime seal-domain identity'
   gregalectl compute-nodes drain-status --node <fqdn>
   ANSIBLE_LIMIT=<host> make ANSIBLE_INVENTORY=<inventory> bootstrap-compute
   ssh <host> 'sudo systemctl restart faas-vmmd'
   gregalectl compute-nodes activate --node <fqdn> --reason 'runtime seal-domain identity staged'
   ```

   Repeat `drain-status` until it exits 0 before you converge.

Check:

- Every compute host's `faas_seal_domain_runtime.prom` shows
  `faas_seal_domain_ready{domain="runtime"} 1`, with the same recipient
  everywhere.
- In Prometheus, `faas_seal_domain_loaded{domain="runtime"}` is 1 on every
  vmmd, and FaasSealDomainIdentityMissing does not fire.
- vmmd logs `"runtime_domain":true` in `vmmd: unseal identities loaded`.
- apid logs `"runtime seal domain"` with `"write_mode":""` and
  `"domain_recipient_staged":true`.

Rollback: nothing is sealed to the domain yet. vmmd holding an extra key
changes nothing, and rolling a release back is safe.

### 4c. Wait out the rollback window

Go on only when all of these hold:

- every vmmd reports `faas_seal_domain_loaded{domain="runtime"} 1`;
- vmmd's previous release (N-1) also carries the runtime reader, so rolling
  back one release keeps it;
- you have a way to stage `runtime.age` on every compute node that joins
  or is rebuilt later. CD and node join do not stage it yet. A node without
  it fails every wake for an app with a secret once 4d has run. Join such a
  node drained, run 4b's compute steps for it, and activate it only once
  it reports `faas_seal_domain_loaded{domain="runtime"} 1`.

### 4d. Seal to the domain only; re-seal stored secrets

Add to the local variables file, and converge the control plane again:

```yaml
faas_seal_domain_runtime_readers_verified: true
faas_seal_domain_runtime_writes: domain
```

apid then logs `"runtime seal domain"` with `"write_mode":"domain"`, and its
rekey runner stops re-sealing runtime secrets. Set a secret on a test app
and confirm the app wakes and reads the value.

Then re-seal the stored secrets. Unlike the other commands in this runbook,
run this one on a compute host: it needs `runtime.age`, which control-plane
hosts do not hold. That host's `gregalectl` must not be newer than the data
release, because `migrate` applies pending migrations first.

```bash
sudo gregalectl seal-domain migrate --domain runtime --readers-verified --db-env /etc/faas/compute-db.env
```

One pass re-seals app and project environment secrets, staged release
bundles, deployment snapshots, and release and promotion captures. It
changes only each envelope's key label and ciphertext, so secret versions
and fingerprints stay as they were.

- Expect `0 failed`.
- If `changed concurrently` is not 0, a customer changed that secret during
  the run; run the migration again.
- A complete migration lists nothing, so a second run reports `0 scanned`.
- If `failed` is not 0, no key on the host opens those envelopes. Those
  secrets were already broken for their customers; investigate with
  [FaasFleetSealDomain](FaasFleetSealDomain.md#recover).

Then watch wake errors across the compute nodes for the next hour. A node
that fails wakes for apps with secrets is missing `runtime.age`: see
[FaasSealDomain](FaasSealDomain.md).

Rollback: set `faas_seal_domain_runtime_writes` back to empty and converge
the control plane. New secrets go back to the fleet key. Secrets re-sealed in
this step stay domain-only. From now on:

- Never remove `runtime.age` from a compute host.
- Never roll vmmd back past the first release that carried the runtime
  reader.
- Never activate a compute node before it reports
  `faas_seal_domain_loaded{domain="runtime"} 1`.
