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

  The control-plane CD then stages the `outbound` and `source` identities
  and every domain's recipient. The compute join stages the `runtime` and
  `build` identities on each node it joins, and leaves the node drained if
  `seal-domain verify` fails there. Before apid seals to a domain, the role
  checks that the staged recipient is the one readers published the probe
  with, so a recipient from another pair stops the converge.
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
Secrets stored before this step stay unbound until a later binding pass of
`seal-domain migrate`.

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
