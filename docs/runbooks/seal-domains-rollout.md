# Seal domains rollout

Turns on what [ADR 20261022111812](../adr/20261022111812-seal-domains-per-reader.md)
(seal domains, first slice) and
[ADR 20261022140000](../adr/20261022140000-break-glass-controls.md)
(break-glass controls) shipped:

1. seal the existing GitHub webhook secrets;
2. move customer outbound credentials to the `outbound` seal domain;
3. rehearse the break-glass release pause and credential revocation;
4. move customer runtime secrets (env vars and secrets) to the `runtime` seal
   domain;
5. move GitHub install tokens and webhook secrets to the `source` seal domain;
6. move private-registry passwords to the `build` seal domain.

Do each stage in staging first, then production. Stages 1 and 3 stand alone.
The steps of stages 2, 4, 5 and 6 run in order, and each has its own
rollback.
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

## Where the keys and settings live

Each stage below shows a converge from your machine with a local variables
file. That works anywhere. Production should use CD instead: CD then also
stages the keys on every host it joins or rebuilds.

- **Keys:** after `gregalectl seal-domain init --domain <domain>`, store the
  pair as GitHub environment secrets:
  - `SEAL_DOMAIN_<DOMAIN>_AGE_KEY`: the contents of `<domain>.age`;
  - `SEAL_DOMAIN_<DOMAIN>_AGE_RECIPIENT`: the contents of `<domain>.age.pub`.

  The control-plane CD then stages the `outbound`, `source`, `alerting`,
  `storage`, `control`, `delivery` and `account` identities and every
  domain's recipient. The compute join stages the `runtime`, `build`, `edge`,
  `control` and `delivery` identities on each node it joins, and leaves the
  node drained if `seal-domain verify` fails there. Before apid seals to a
  domain, the role checks that the staged recipient is the one readers
  published the probe with, so a recipient from another pair stops the
  converge.
- **Settings** (`faas_seal_domain_<domain>_writes` and
  `faas_seal_domain_<domain>_readers_verified`) go in `CP_ANSIBLE_VARS_B64`.
  CD re-renders the drop-ins from them on every converge, so a value set
  only in a local file is reverted.
- **Never** put `faas_seal_domains_managed` or a `*_src` variable in CD's
  variables. CD sets them from the secrets and refuses a file that already
  has them.

Wherever a stage says to add `*_src` lines to the local variables file, with
CD you set the domain's secrets instead. Wherever it says to set
`*_writes` or `*_readers_verified`, you add them to `CP_ANSIBLE_VARS_B64` and
run the control-plane CD.

## 1. Seal the GitHub webhook secrets

apid writes only a sealed copy whenever a webhook secret is set. This stage
backfills the copy for older rows, which have only the plaintext. The copy is
sealed to the fleet recipient, which githubd already holds as `fleet.age`.

Every environment must run this stage before the release that drops the
plaintext column (ADR 20261022111812): after that, a row without a sealed
copy has no secret left.

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

Rollback: none needed. The backfill only adds copies; the plaintext stays
until the column is dropped.

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

With CD, set the `SEAL_DOMAIN_OUTBOUND_*` secrets instead of these lines.
Without it, keep these lines out of `CP_ANSIBLE_VARS_B64`. The CD runner
does not have the files, so the role would fail every control-plane
release. While `faas_seal_domains_managed` is unset, CD's converge skips the
role and leaves the staged files and drop-ins in place.

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
- A new or rebuilt control-plane host gets `outbound.age` from CD once the
  `SEAL_DOMAIN_OUTBOUND_*` secrets are set. Without them, run the
  `seal_domains` role for it from the local variables file before its
  outboundd serves traffic.

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

With CD, set the `SEAL_DOMAIN_RUNTIME_*` secrets instead of these lines. The
next control-plane CD stages the recipient, and every compute join stages
`runtime.age`. Nodes already in service still need the compute steps below,
or a fresh join. Without CD, as in 2b, keep these lines out of the variables
CD uses (`CP_ANSIBLE_VARS_B64` and `COMPUTE_ANSIBLE_VARS_B64`).

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
- every compute node that joins or is rebuilt later will get `runtime.age`.
  A node without it fails every wake for an app with a secret once 4d has
  run. With the `SEAL_DOMAIN_RUNTIME_*` secrets set, CD's join stages and
  verifies it, and fails the join otherwise. Without them, join such a node
  drained, run 4b's compute steps for it, and activate it only once it
  reports `faas_seal_domain_loaded{domain="runtime"} 1`.

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

### 4e. Bind new runtime secrets to their account

[ADR 20261023173624](../adr/20261023173624-runtime-secrets-bound-to-their-account.md)
binds each runtime secret to its account, so vmmd opens it only for that
account's workloads. This step is independent of 4a–4d: it works whether
runtime secrets are sealed to the fleet key or the domain.

A vmmd from before the binding refuses a bound envelope, and so do
gregalectl and apid from before it. Wait until every compute node, every
control-plane node and the operator machines run the release that carries
the binding check, and that release's predecessor has left the rollback
window. Then add to the local variables file and converge the control plane:

```yaml
faas_seal_domain_runtime_binding_readers_verified: true
faas_seal_domain_runtime_binding: account
```

apid logs `"runtime seal domain"` with `"account_binding":true`. Set a
secret on a test app and confirm it wakes and reads the value. A wake that
fails with `envelope is bound to another account` means a secret row was
copied to an app of another account; do not re-seal it. Find how it got
there.

Rollback: set `faas_seal_domain_runtime_binding: off` and converge. New
secrets are written unbound again. Secrets written while binding was on stay
bound, so never roll a reader back past the release that carries the check.
Secrets stored before this step stay unbound until 4f binds them.

### 4f. Bind stored runtime secrets; then require bindings

This step needs 4d and 4e both complete: runtime secrets are sealed to the
domain alone, and apid binds every new one. The pass re-seals to the domain
recipient alone, so it has 4d's precondition too.

On a compute host, as in 4d (it needs `runtime.age`, and its `gregalectl`
must not be newer than the data release):

```bash
sudo gregalectl seal-domain migrate --domain runtime --readers-verified --bind-accounts --db-env /etc/faas/compute-db.env
```

It reads every stored runtime secret: app and project environment secrets,
staged release bundles, deployment snapshots, and release and promotion
captures. It re-seals each unbound envelope bound to the account that owns
its row. Secret versions and fingerprints stay as they were.

- Run it again until it reports `0 re-sealed`. `changed concurrently` means
  a customer changed that secret during the run.
- `bound to another account` must be 0. Each such document is listed: its
  secret was copied from another account's row. Do not re-seal it, and do
  not go on. Find how it got there, as in 4e.
- `failed` must be 0. A failed document either holds an envelope no key on
  the host opens (see 4d) or has no owning row.

Then restart schedd on every node, one at a time. A re-seal of a project
secret or a deployment snapshot does not invalidate schedd's owner cache,
so a schedd that has not restarted can still send ciphertext from before
the pass.

Last, require bindings on every compute host. Add to the local variables
file and converge the compute nodes:

```yaml
faas_seal_domain_runtime_binding_required: true
```

The role writes vmmd's drop-in but does not restart vmmd. vmmd applies it at
its next start (the next release, or a planned restart), and
`faas_runtime_secret_binding_required` says which vmmds enforce it. From then
on a wake whose secret is unbound fails with `envelope is not bound to an
account`. If one does, a writer is still sealing unbound: check
`faas_seal_domain_runtime_binding` on that control plane, run the pass
again, and restart schedd.

Rollback: set `faas_seal_domain_runtime_binding_required: false`, converge
the compute nodes, and restart vmmd. Bound secrets stay bound.

## 5. Source seal domain

githubd is the only reader of GitHub install tokens and webhook secrets, and
it seals the install tokens itself. apid seals the webhook secret copies. At
the end of this stage only `source.age` opens them, and only githubd holds
it.

Install tokens need no migration. Each expires within the hour, and githubd
seals its successor under the write mode. Webhook secrets keep their
plaintext column until a later release drops it; this stage moves their
sealed copy ahead of that.

### 5a. Create the identity (operator machine)

```bash
gregalectl seal-domain init --domain source --dir ~/gregale-seal/<environment>
```

Store `source.age` with the other domain identities before going on. If it
is lost after 5d, GitHub pushes and source fetches fail. Install tokens
recover within the hour as they expire and githubd mints new ones, but every
webhook secret must be set again.

### 5b. Stage the reader; seal to both keys

Add to the local variables file:

```yaml
faas_seal_domain_source_identity_src: /Users/<you>/gregale-seal/<environment>/source.age
faas_seal_domain_source_recipient_src: /Users/<you>/gregale-seal/<environment>/source.age.pub
```

Converge the role alone:

```bash
gregalectl deploy converge-control-plane --roles seal_domains --manifest-file <signed manifest> --ssh-known-hosts-file <known_hosts> --ansible-vars-file <local vars file>
```

The role does these steps in order:

1. Installs `source.age` for githubd and restarts it.
2. Proves the domain probe, publishing it on the first host.
3. Only then gives apid the recipient.

With `faas_seal_domain_source_writes` left empty, githubd and apid seal to
both the domain and the fleet key.

If more than one host runs githubd, converge each one before step 5d.

Check, on each host:

- `faas_seal_domain_source.prom` shows `faas_seal_domain_ready{domain="source"} 1`.
- githubd logs `"source seal domain"` with `"domain_identity":true`, and
  `faas_seal_domain_loaded{domain="source"}` is 1.
- apid logs `"source seal domain"` with `"domain_recipient_staged":true` and
  `"recipients":2`.
- Push to a connected repository and confirm the build starts. That one
  delivery checks the webhook secret and an install token.

Rollback: set `faas_seal_domain_source_writes: fleet` and converge the role
again. Everything sealed in the meantime also carries the fleet key.

### 5c. Wait out the rollback window

Go on only when every githubd host passed the checks in 5b, and githubd's
previous release (N-1) also carries the source reader.

### 5d. Seal to the domain only; re-seal stored copies

Add to the local variables file, and converge the role again:

```yaml
faas_seal_domain_source_readers_verified: true
faas_seal_domain_source_writes: domain
```

githubd and apid then log `"write_mode":"domain"`. Re-seal the webhook
secret copies on the control-plane host:

```bash
sudo gregalectl seal-domain migrate --domain source --readers-verified --db-env /etc/faas/compute-db.env
```

- Expect `0 failed`.
- If `changed concurrently` is not 0, an admin rotated that secret during
  the run; run the migration again.
- A second run reports every row `current`.
- If `failed` is not 0, githubd cannot open those copies today either; set
  those webhook secrets again.

Within an hour, githubd has re-sealed every live install token to the
domain. Push to a connected repository again to confirm.

Rollback: set `faas_seal_domain_source_writes` back to empty and converge.
New tokens and copies go back to carrying the fleet key. Copies re-sealed in
this step stay domain-only, so:

- never remove `source.age` from githubd;
- never roll githubd back past the first release that carried the source
  reader.

## 6. Build seal domain

imaged on compute hosts is the only reader of app and job private-registry
passwords; apid seals them. At the end of this stage only `build.age` opens
them, and only imaged holds it.

### 6a. Create the identity (operator machine)

```bash
gregalectl seal-domain init --domain build --dir ~/gregale-seal/<environment>
```

Store `build.age` with the other domain identities. If it is lost after 6d,
customers must set their registry credentials again.

### 6b. Stage the readers; seal to both keys

Add to the local variables file:

```yaml
faas_seal_domain_build_identity_src: /Users/<you>/gregale-seal/<environment>/build.age
faas_seal_domain_build_recipient_src: /Users/<you>/gregale-seal/<environment>/build.age.pub
```

1. **Each compute host:**
   `ANSIBLE_LIMIT=<host> make ANSIBLE_INVENTORY=<inventory> bootstrap-compute`.
   The role:
   - installs `build.age`;
   - restarts imaged (so an image preparation in flight on that host fails
     and is retried);
   - proves the probe, publishing it on the first host.

   Converge one host at a time.
2. **Control plane:** converge the role alone. With
   `faas_seal_domain_build_writes` left empty, apid seals new passwords to
   both the domain and the fleet key, so this step and step 1 can run in
   either order.

   ```bash
   gregalectl deploy converge-control-plane --roles seal_domains --manifest-file <signed manifest> --ssh-known-hosts-file <known_hosts> --ansible-vars-file <local vars file>
   ```

Check:

- Every compute host's `faas_seal_domain_build.prom` shows
  `faas_seal_domain_ready{domain="build"} 1`.
- In Prometheus, `faas_seal_domain_loaded{domain="build"}` is 1 on every
  imaged, and FaasSealDomainIdentityMissing does not fire.
- imaged logs `"build seal domain"` with `"domain_identity":true`.
- apid logs `"build seal domain"` with `"recipients":2`.
- Deploy a test app that pulls from a private registry, with a credential
  set after the converge.

Rollback: set `faas_seal_domain_build_writes: fleet` and converge the control
plane. Passwords saved in the meantime also carry the fleet key.

### 6c. Wait out the rollback window

Go on only when every imaged reports `faas_seal_domain_loaded{domain="build"} 1`
and imaged's previous release (N-1) also carries the build reader. As with
`runtime.age`, a compute node that joins later needs `build.age` before it
prepares images. With the `SEAL_DOMAIN_BUILD_*` secrets set, CD's join stages
and verifies it; without them, stage it by hand before activating the
node.

### 6d. Seal to the domain only; re-seal stored passwords

Add to the local variables file, and converge the control plane:

```yaml
faas_seal_domain_build_readers_verified: true
faas_seal_domain_build_writes: domain
```

apid then logs `"write_mode":"domain"`. Re-seal on a compute host, which
holds `build.age` (its `gregalectl` must not be newer than the data
release):

```bash
sudo gregalectl seal-domain migrate --domain build --readers-verified --db-env /etc/faas/compute-db.env
```

- Expect `0 failed`.
- If `changed concurrently` is not 0, a customer changed that credential
  during the run; run the migration again.
- A second run reports every row `current`.
- If `failed` is not 0, imaged cannot open those passwords today either;
  their owners must set them again.

Deploy the test app again.

Rollback: set `faas_seal_domain_build_writes` back to empty and converge.
Passwords re-sealed in this step stay domain-only, so:

- never remove `build.age` from a compute host;
- never roll imaged back past the first release that carried the build
  reader.

## 7. Edge seal domain

gatewayd-internal on compute hosts is the only reader of app basic-auth
credentials and log drain authentication headers; apid seals them. At the
end of this stage only `edge.age` opens them, except the log destinations
of application standards and the drains those standards manage, which stay
on the fleet key for now (ADR 20261022111812, fifth slice).

### 7a. Create the identity (operator machine)

```bash
gregalectl seal-domain init --domain edge --dir ~/gregale-seal/<environment>
```

Store `edge.age` with the other domain identities. If it is lost after 7d,
customers must set their basic-auth credentials and log drain headers
again.

### 7b. Stage the readers; seal to both keys

Add to the local variables file, or set the `SEAL_DOMAIN_EDGE_*` secrets:

```yaml
faas_seal_domain_edge_identity_src: /Users/<you>/gregale-seal/<environment>/edge.age
faas_seal_domain_edge_recipient_src: /Users/<you>/gregale-seal/<environment>/edge.age.pub
```

1. **Each compute host:**
   `ANSIBLE_LIMIT=<host> make ANSIBLE_INVENTORY=<inventory> bootstrap-compute`.
   The role:
   - installs `edge.age`;
   - restarts gatewayd-internal, as any gatewayd-internal configuration
     change does;
   - proves the probe, publishing it on the first host.

   Converge one host at a time.
2. **Control plane:** converge the role alone. With
   `faas_seal_domain_edge_writes` left empty, apid seals new credentials to
   both the domain and the fleet key, so this step and step 1 can run in
   either order.

   ```bash
   gregalectl deploy converge-control-plane --roles seal_domains --manifest-file <signed manifest> --ssh-known-hosts-file <known_hosts> --ansible-vars-file <local vars file>
   ```

Check:

- Every compute host's `faas_seal_domain_edge.prom` shows
  `faas_seal_domain_ready{domain="edge"} 1`.
- In Prometheus, `faas_seal_domain_loaded{domain="edge"}` is 1 on every
  gatewayd-internal, and FaasSealDomainIdentityMissing does not fire.
- gatewayd-internal logs `"edge seal domain"` with `"domain_identity":true`.
- apid logs `"edge seal domain"` with `"recipients":2`.
- On a test app, set basic auth after the converge and request it with the
  credentials; add a log drain with an authentication header and check
  that its receiver gets lines.

Rollback: set `faas_seal_domain_edge_writes: fleet` and converge the control
plane. Credentials saved in the meantime also carry the fleet key.

### 7c. Wait out the rollback window

Go on only when every gatewayd-internal reports
`faas_seal_domain_loaded{domain="edge"} 1` and gatewayd-internal's previous
release (N-1) also carries the edge reader. A compute node that joins later
needs `edge.age` before it serves traffic. With the `SEAL_DOMAIN_EDGE_*`
secrets set, CD's join stages and verifies it; without them, stage it by
hand before activating the node.

### 7d. Seal to the domain only; re-seal stored credentials

Add to the local variables file, and converge the control plane:

```yaml
faas_seal_domain_edge_readers_verified: true
faas_seal_domain_edge_writes: domain
```

apid then logs `"write_mode":"domain"`. Re-seal on a compute host, which
holds `edge.age` (its `gregalectl` must not be newer than the data
release):

```bash
sudo gregalectl seal-domain migrate --domain edge --readers-verified --db-env /etc/faas/compute-db.env
```

- Expect `0 failed`.
- `changed concurrently` counts rows a customer changed during the run,
  and rows a guard held: a project clone in progress or a pending standard
  review. Run the migration again later.
- `left on the fleet key` counts application-standard log destinations and
  the drains they manage. They are expected and keep working; a later
  release re-seals them.
- A second run reports every other row `current`.
- If `failed` is not 0, gatewayd-internal cannot open those credentials
  today either; their owners must set them again.

Request the test app again, and check the drain still delivers.

Rollback: set `faas_seal_domain_edge_writes` back to empty and converge.
Credentials re-sealed in this step stay domain-only, so:

- never remove `edge.age` from a compute host;
- never roll gatewayd-internal back past the first release that carried
  the edge reader.

## 8. Alerting seal domain

meterd on the control plane is the only reader of alert rule webhook
secrets, notification channel destinations (Slack URLs, PagerDuty routing
keys) and MCP readiness PagerDuty and fallback keys; apid seals them. At the
end of this stage only `alerting.age` opens them, and only meterd holds it.
Customer test sends that apid cannot open go through meterd (ADR
20261022111812, section 3).

### 8a. Create the identity (operator machine)

```bash
gregalectl seal-domain init --domain alerting --dir ~/gregale-seal/<environment>
```

Store `alerting.age` with the other domain identities. If it is lost after
8d, customers must set their alert webhook secrets, channel destinations and
MCP readiness keys again.

### 8b. Stage the reader; seal to both keys

Set the `SEAL_DOMAIN_ALERTING_*` secrets, or add to the local variables
file:

```yaml
faas_seal_domain_alerting_identity_src: /Users/<you>/gregale-seal/<environment>/alerting.age
faas_seal_domain_alerting_recipient_src: /Users/<you>/gregale-seal/<environment>/alerting.age.pub
```

Then converge the role on the control plane:

```bash
gregalectl deploy converge-control-plane --roles seal_domains --manifest-file <signed manifest> --ssh-known-hosts-file <known_hosts> --ansible-vars-file <local vars file>
```

As for outbound, the role installs `alerting.age`, restarts meterd, proves
the probe, and only then gives apid the recipient. With
`faas_seal_domain_alerting_writes` left empty, apid seals new values to both
the domain and the fleet key.

Check, on each control-plane host:

- `faas_seal_domain_alerting.prom` shows
  `faas_seal_domain_ready{domain="alerting"} 1`.
- In Prometheus, `faas_seal_domain_loaded{domain="alerting"}` is 1 on every
  meterd.
- meterd logs `"alerting seal domain"` with `"domain_identity":true`.
- apid logs `"alerting seal domain"` with `"recipients":2`.
- On a test account, create a Slack channel and an alert rule with a
  webhook, and run both test sends. They still go out from apid, which can
  open values sealed to both keys.

Rollback: set `faas_seal_domain_alerting_writes: fleet` and converge.

### 8c. Wait out the rollback window

Go on only when every meterd reports `faas_seal_domain_loaded{domain="alerting"} 1`
and meterd's previous release (N-1) also carries the alerting reader and the
test-send relay.

### 8d. Seal to the domain only; re-seal stored values

Add to the local variables file, and converge the control plane:

```yaml
faas_seal_domain_alerting_readers_verified: true
faas_seal_domain_alerting_writes: domain
```

apid then logs `"write_mode":"domain"`. Re-seal on a control-plane host,
which holds `alerting.age`:

```bash
sudo gregalectl seal-domain migrate --domain alerting --readers-verified --db-env /etc/faas/compute-db.env
```

- Expect `0 failed`.
- If `changed concurrently` is not 0, a customer changed that value during
  the run; run the migration again.
- A second run reports every row `current`.
- If `failed` is not 0, meterd cannot open those values today either; their
  owners must set them again.

Run the test sends from 8b again. apid can no longer open the values, so
meterd sends them; a 504 means meterd did not pick the request up within
the wait (check meterd's `"meterd: alert test sends"` log lines).

Open MCP readiness incidents keep a copy of their route's key in
`delivery_routing`, sealed as it was when the incident opened. Those copies
stay readable while meterd keeps the fleet identity, and go away as the
incidents close.

Rollback: set `faas_seal_domain_alerting_writes` back to empty and converge.
Values re-sealed in this step stay domain-only, so:

- never remove `alerting.age` from a control-plane host;
- never roll meterd back past the first release that carried the alerting
  reader.

## 9. Storage seal domain

s3-gatewayd on the control plane is the only reader of Gregale S3 secret
access keys (customer credentials, compute bindings and signed URLs); apid
seals them. At the end of this stage only `storage.age` opens them, and only
s3-gatewayd holds it.

s3-gatewayd re-seals, at every start, any credential whose key id is not its
current key. With the domain staged it leaves the credentials the domain
opens alone, and apid files dual-sealed credentials under the fleet key id,
so no gateway re-keys them. Moving credentials into the domain is 9d's
migration, never a restart.

### 9a. Create the identity (operator machine)

```bash
gregalectl seal-domain init --domain storage --dir ~/gregale-seal/<environment>
```

Store `storage.age` with the other domain identities. If it is lost after
9d, every S3 credential must be reissued.

### 9b. Stage the reader; seal to both keys

Set the `SEAL_DOMAIN_STORAGE_*` secrets, or add to the local variables file:

```yaml
faas_seal_domain_storage_identity_src: /Users/<you>/gregale-seal/<environment>/storage.age
faas_seal_domain_storage_recipient_src: /Users/<you>/gregale-seal/<environment>/storage.age.pub
```

Then converge the role on the control plane:

```bash
gregalectl deploy converge-control-plane --roles seal_domains --manifest-file <signed manifest> --ssh-known-hosts-file <known_hosts> --ansible-vars-file <local vars file>
```

As for outbound, the role installs `storage.age`, restarts s3-gatewayd,
proves the probe, and only then gives apid the recipient.

Check, on each control-plane host:

- `faas_seal_domain_storage.prom` shows
  `faas_seal_domain_ready{domain="storage"} 1`.
- In Prometheus, `faas_seal_domain_loaded{domain="storage"}` is 1 on every
  s3-gatewayd.
- s3-gatewayd logs `"storage seal domain"` with `"domain_identity":true`.
- apid logs `"storage seal domain"` with `"recipients":2`.
- Create an S3 credential on a test bucket, and use it and an older one.

Rollback: set `faas_seal_domain_storage_writes: fleet` and converge.

### 9c. Wait out the rollback window

Go on only when every s3-gatewayd reports `faas_seal_domain_loaded{domain="storage"} 1`
and s3-gatewayd's previous release (N-1) also carries the storage reader. An
older s3-gatewayd refuses to start over a credential it cannot open.

### 9d. Seal to the domain only; re-seal stored credentials

Add to the local variables file, and converge the control plane:

```yaml
faas_seal_domain_storage_readers_verified: true
faas_seal_domain_storage_writes: domain
```

Re-seal on a control-plane host, which holds `storage.age`:

```bash
sudo gregalectl seal-domain migrate --domain storage --readers-verified --db-env /etc/faas/compute-db.env
```

- Expect `0 failed`.
- `changed concurrently` counts credentials rotated during the run, and
  ones a project clone in progress holds; run the migration again later.
- A second run reports every row `current`.
- Revoked credentials and expired signed URLs no longer authenticate and
  are left as they are.

Re-sealing a credential bumps the binding promotion revision of its app, as
for outbound; run this step at a quiet hour. Use the test credentials from
9b again.

Rollback: set `faas_seal_domain_storage_writes` back to empty and converge.
Credentials re-sealed in this step stay domain-only, so:

- never remove `storage.age` from a control-plane host;
- never roll s3-gatewayd back past the first release that carried the
  storage reader.

## 10. Control seal domain

schedd is the only reader of the cluster signing key, which it uses to mint
the internal service tokens every other daemon verifies. schedd runs on
control-plane and compute hosts alike, so every host stages `control.age`.
apid never writes the key, so this domain has no recipient on apid and no
write mode: the key moves into the domain only in 10c.

`gregalectl fleet-seal migrate` leaves a cluster key the control identity
opens where it is, and `fleet-seal verify`, which node join runs, opens it
with the control identity when the fleet key no longer does. Both read
`/etc/faas/secrets/domains/control.age` when it is staged (`--control-key`
overrides the path).

### 10a. Create the identity (operator machine)

```bash
gregalectl seal-domain init --domain control --dir ~/gregale-seal/<environment>
```

Store `control.age` with the other domain identities. If it is lost after
10c, the cluster signing key must be replaced, which re-issues every
internal service token.

### 10b. Stage every schedd

Set the `SEAL_DOMAIN_CONTROL_*` secrets (CD stages the identity on the
control plane and on every compute join), or add to the local variables
file:

```yaml
faas_seal_domain_control_identity_src: /Users/<you>/gregale-seal/<environment>/control.age
```

and converge the role on the control plane and on each compute host, one at
a time. The role installs `control.age`, restarts schedd and proves the
probe.

Check:

- every host's `faas_seal_domain_control.prom` shows
  `faas_seal_domain_ready{domain="control"} 1`;
- `faas_seal_domain_loaded{domain="control"}` is 1 on every schedd, and
  FaasSealDomainIdentityMissing does not fire;
- schedd logs `"control seal domain"` with `"domain_identity":true`.

Go on only when every schedd, on both node roles, reports the identity
loaded, and schedd's previous release (N-1) also carries the control reader.

### 10c. Re-seal the cluster signing key

On any host that holds `control.age` (its `gregalectl` must not be newer
than the data release):

```bash
sudo gregalectl seal-domain migrate --domain control --readers-verified --db-env /etc/faas/compute-db.env
```

- Expect `1 re-sealed` (or `1 current` on a second run).
- Then run `sudo gregalectl fleet-seal verify --db-env /etc/faas/compute-db.env`
  on one host of each role: it must report `ready=true` and
  `"cluster_key_sealed_to":"control"`.
- Restart one schedd and confirm it starts and mints tokens (its logs show
  no `cluster_signing_keys sealed_blob is not unsealable` line).

Rollback: none by migration; the key stays domain-only. From now on:

- never remove `control.age` from a host that runs schedd;
- a node that joins needs `control.age` before activation: with the
  `SEAL_DOMAIN_CONTROL_*` secrets set, CD's join stages it, and the join's
  `fleet-seal verify` fails without it;
- never roll schedd back past the first release that carried the control
  reader.

## 11. Delivery seal domain

schedd is the only reader of app webhook signing secrets, commit-source
connections and execution payloads; apid seals them. schedd runs on
control-plane and compute hosts alike, so every host stages
`delivery.age`, and the control plane also stages the recipient for apid.
At the end of this stage only `delivery.age` opens them, except trigger
configs, which stay on the fleet key for now (ADR 20261022111812, ninth
slice).

### 11a. Create the identity (operator machine)

```bash
gregalectl seal-domain init --domain delivery --dir ~/gregale-seal/<environment>
```

Store `delivery.age` with the other domain identities. If it is lost after
11d, customers must set their webhook signing secrets and commit-source
connections again, and queued executions fail.

### 11b. Stage every schedd; seal to both keys

Set the `SEAL_DOMAIN_DELIVERY_*` secrets (CD stages the identity on the
control plane and on every compute join, and the recipient on the control
plane), or add to the local variables file:

```yaml
faas_seal_domain_delivery_identity_src: /Users/<you>/gregale-seal/<environment>/delivery.age
faas_seal_domain_delivery_recipient_src: /Users/<you>/gregale-seal/<environment>/delivery.age.pub
```

and converge the role on the control plane and on each compute host, one at
a time. On every host the role installs `delivery.age`, restarts schedd and
proves the probe, publishing it on the first host. On the control plane it
then has apid seal new values to the domain and the fleet key, so the order
of the converges does not matter.

Check:

- every host's `faas_seal_domain_delivery.prom` shows
  `faas_seal_domain_ready{domain="delivery"} 1`;
- `faas_seal_domain_loaded{domain="delivery"}` is 1 on every schedd, and
  FaasSealDomainIdentityMissing does not fire;
- schedd logs `"delivery seal domain"` with `"domain_identity":true`;
- apid logs `"delivery seal domain"` with `"recipients":2`;
- on a test app, rotate a webhook's signing secret and check that the next
  delivery verifies; run an execution and check that it succeeds.

Rollback: set `faas_seal_domain_delivery_writes: fleet` and converge the
control plane. Values saved in the meantime also carry the fleet key, and
payloads are filed under the fleet key id.

### 11c. Wait out the rollback window

Go on only when every schedd, on both node roles, reports
`faas_seal_domain_loaded{domain="delivery"} 1`, and schedd's previous
release (N-1) also carries the delivery reader. A compute node that joins
later needs `delivery.age` before it is activated: with the
`SEAL_DOMAIN_DELIVERY_*` secrets set, CD's join stages and verifies it;
without them, stage it by hand before activating the node.

### 11d. Seal to the domain only; re-seal stored values

Add to the local variables file, and converge the control plane:

```yaml
faas_seal_domain_delivery_readers_verified: true
faas_seal_domain_delivery_writes: domain
```

apid then logs `"write_mode":"domain"`. Re-seal on any host that holds
`delivery.age` (its `gregalectl` must not be newer than the data release):

```bash
sudo gregalectl seal-domain migrate --domain delivery --readers-verified --db-env /etc/faas/compute-db.env
```

- Expect `0 failed`.
- `changed concurrently` counts values a customer rotated during the run,
  and payloads of executions that started meanwhile. Run the migration
  again later.
- A second run reports every row `current`.
- Payloads of finished executions are not scanned; retention deletes them.
- If `failed` is not 0, schedd cannot open those values today either:
  their owners must set the webhook secret or connection again.

Rotate the test webhook's secret again and check the next delivery
verifies; run an execution.

Rollback: set `faas_seal_domain_delivery_writes` back to empty and
converge. Values re-sealed in this step stay domain-only, so:

- never remove `delivery.age` from a host that runs schedd;
- never roll schedd back past the first release that carried the delivery
  reader.

## 12. Account seal domain

apid both seals and opens account values, so it holds `account.age` itself;
there is no recipient file. It covers TOTP secrets, inbound webhook
secrets, billing provider keys, managed workflow plans, PagerDuty
acknowledgement secrets, GitLab, Slack and Linear credentials, product
email values and managed realtime credentials. The project clone columns
stay on the fleet key until a later release (ADR 20261022111812, tenth to
thirteenth slices). Data pipeline values are not part of this stage: they
have always been sealed to the pipeline workers' own `pipeline.age`.

### 12a. Create the identity (operator machine)

```bash
gregalectl seal-domain init --domain account --dir ~/gregale-seal/<environment>
```

Store `account.age` with the other domain identities. If it is lost after
12d, every customer with MFA must enroll again; inbound webhooks, billing
connections and GitLab, Slack and Linear connections must be set again; and
queued managed workflows fail; managed realtime endpoints need new tokens
and push devices must register again.

### 12b. Stage apid; seal to both keys

Set the `SEAL_DOMAIN_ACCOUNT_*` secrets (CD stages the identity on the
control plane only), or add to the local variables file:

```yaml
faas_seal_domain_account_identity_src: /Users/<you>/gregale-seal/<environment>/account.age
```

and converge the role on the control plane. The role installs `account.age`,
restarts apid with it and proves the probe. With
`faas_seal_domain_account_writes` left empty, apid then seals new account
values to the domain and the fleet key.

Check:

- `faas_seal_domain_account.prom` shows
  `faas_seal_domain_ready{domain="account"} 1`;
- `faas_seal_domain_loaded{domain="account"}` is 1 on every apid, and
  FaasSealDomainIdentityMissing does not fire;
- apid logs `"account seal domain"` with `"domain_identity":true`;
- on a test account, enroll MFA and sign in with it; send a test delivery
  to an inbound webhook; send a test issue to a connected Slack workspace.

Rollback: set `faas_seal_domain_account_writes: fleet` and converge. Values
saved in the meantime also carry the fleet key.

### 12c. Wait out the rollback window

Go on only when every apid reports `faas_seal_domain_loaded{domain="account"} 1`
and apid's previous release (N-1) also carries the account reader.

### 12d. Seal to the domain only; re-seal stored values

Add to the local variables file, and converge the control plane:

```yaml
faas_seal_domain_account_readers_verified: true
faas_seal_domain_account_writes: domain
```

apid then logs `"write_mode":"domain"`. Re-seal on the control-plane host:

```bash
sudo gregalectl seal-domain migrate --domain account --readers-verified --db-env /etc/faas/compute-db.env
```

- Expect `0 failed`.
- `changed concurrently` counts values a customer changed during the run.
  Run the migration again later.
- A second run reports every row `current`.
- If `failed` is not 0, apid cannot open those values today either: the
  owners must set them again (MFA: enroll again).

Sign in with MFA on the test account again.

Rollback: set `faas_seal_domain_account_writes` back to empty and converge.
Values re-sealed in this step stay domain-only, so:

- never remove `account.age` from a control-plane host;
- never roll apid back past the first release that carried the account
  reader.
