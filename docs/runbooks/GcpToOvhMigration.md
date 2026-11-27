# Moving production from GCP to OVHcloud bare metal (ADR-1344 §8)

This runbook moves the whole fleet: the shared artifact store, the control
plane, the compute nodes, app traffic, the log archive and the off-host
backups. It strings together runbooks that already exist and adds the steps
only this move needs. Read each linked runbook before the day.

**The control plane moves with downtime** (owner decision, 2026-11-19).
GCP stops writing, a final backup is taken, and the OVH control node is
rebuilt from it with the existing rebuild procedure
(`ControlPlaneStateBackup.md`, "Rebuild a control plane from backups").
Nothing replicates between the providers. GCP's production configuration
changes only to turn on control-plane state backups, and until OVH serves
traffic the rollback is to start GCP again.

The plan has three phases:

1. **Prepare (days ahead, no customer impact).** The OVH hosts are built and
   joined to nothing. The GCP fleet switches its artifact store to the OVH
   bucket, the old GCS objects are copied over, and the restore is
   rehearsed on the OVH control node.
2. **Cut over (one window).** GCP's control plane stops, the final backups
   are taken and copied to OVH, the OVH control node is rebuilt from them,
   OVH compute nodes join, GCP compute nodes drain, and app DNS moves.
3. **Settle (days after).** GCP is retired, or later rebuilt as a warm
   standby.

What to expect:

- **API downtime from the freeze to the first deploy on OVH**, about one to
  two hours. Most of it is the control node's first bootstrap and the
  PostgreSQL restore. The rehearsal (phase 1 step 9) measured the restore
  at about a minute plus one second per 16 MB WAL segment since the base
  backup. Phase 2 step 3 takes a fresh base backup, so the window replays
  only minutes of WAL. Phase 1 step 10 builds the bootstrap's slowest part
  ahead (10 min 51 s on 2026-11-20; 82 s once current), so the bootstrap
  itself is the one step not yet timed. Estimate for 2026-11-20:

  | Step | Time |
  |---|---|
  | Freeze, final backups, copy to OVH | ~10 min |
  | Restore and install control-plane state, re-seal | ~10 min |
  | Bootstrap, slow software already current | ~20–40 min, untimed |
  | PostgreSQL restore | ~3 min |
  | Deploy the release through CD | ~15 min |
  | Join both compute nodes (budget 105 min each) | ~30–60 min |
  | Move traffic, checks | ~15 min |
- **Warm apps keep serving** from the GCP compute nodes until they drain
  (ADR-1344 §5). A wake needs the control plane's database, so apps that are
  not running fail from the freeze until the OVH compute nodes serve.
- **Nothing written after the final backup survives.** The window stops the
  control plane, takes the backups, then stops GCP's PostgreSQL, so nothing
  can write after the backup.
- **One cold boot per app on its first OVH wake.** GCP production runs
  without a CPU template, so its snapshots belong to the GCP platform's slot
  and no OVH CPU restores them (ADR-20261024225612, `cpu_incompatible` in the
  cold-reason metric). Each app is recaptured on OVH after that boot.

## Why this order

- **No phase runs OVH and GCP hosts against each other over the internet.**
  Compute nodes reach the control plane's Postgres with `sslmode=disable`
  and schedd/vmmd over the private overlay. That is fine on a VPC or a
  vRack, and not across providers. The database crosses providers once, as
  an encrypted backup in a bucket.
- **OVH hosts cannot read GCS.** There is no metadata server off GCP, and
  the `gregale-prod` org policy (`iam.disableServiceAccountKeyCreation`)
  forbids the service-account key a `GOOGLE_APPLICATION_CREDENTIALS`
  fallback would need. So the store moves while the fleet is still on GCP
  (GCP nodes write to the OVH bucket and read misses through their own GCS
  identity, `FAAS_STORAGE_FALLBACK_BACKEND=gcs`), and in the window a GCP
  host copies the backups into the OVH backup bucket.
- **Drained apps park to the shared store.** Once every artifact lives in the
  OVH bucket, a GCP node can drain and its apps wake on OVH from their app
  layers (a cold boot, see above) with nothing left on GCP.
- **A restored copy of production runs no daemon outside the window.** Its
  daemons would act on production data: send mail, fire webhooks, bill. The
  rehearsal restores into a throwaway PostgreSQL only.
- **Customer object storage does not move; it starts empty on OVH.** A
  bucket keeps the backend it was created on, and nothing moves a bucket
  between backends (`docs/ops/object-storage-operator-notes.md`, "Switch
  providers"). GCP's backend, `gcs-us-east1-prod-v1`, signs through the
  GCP machine identity, which no OVH host has, so a GCS bucket left in the
  database would answer 503 after the window. Production's only buckets
  were seven test buckets of the owner's account (13 MB, unused for two
  weeks; customer object storage is not launched). They were emptied and
  deleted on 2026-11-20 with `gregale bucket version delete` and
  `gregale bucket delete`, and the upstream project holds no bucket. Check
  before the window that `object_buckets` has no row outside `deleted`.
  The OVH control plane runs without the S3 gateway until an OVH backend
  is set up (below).

## Go / no-go (all must hold before the window)

- [ ] S3 artifact store (PR #6724, ADR-20261107123000) merged and in the
      release GCP runs.
- [ ] OVH hardware delivered: one control node (≥ 32 GB RAM, NVMe) and the
      compute nodes, all on one vRack. **Compute nodes share one CPU
      signature**: without a CPU template a snapshot restores only on a host
      with the same vendor, family, model, stepping and flag set
      (`SnapshotRestoreCompatible` in `pkg/fcvm/cputemplate.go`), so buy one
      CPU model and keep kernel and microcode in step. A template
      (`vmmd_cpu_template`: T2CL for Cascade/Ice Lake, T2A for Milan) is
      needed only to mix CPU models, and covers Skylake, Cascade Lake, Ice
      Lake and Milan only. The OVH compute nodes (Xeon E-2388G, Rocket Lake)
      run without one; on 2026-11-19 both had the same signature (family 6
      model 167 stepping 1, microcode 0x66, kernel 7.0.0-43).
- [ ] The cold-boot memory headroom fix (#7440, ADR-20261116170000) is in
      the release: every app cold-boots once on cutover day, and 128 MB apps
      ran out of memory on cold boot without it.
- [ ] One OVH Additional IP per compute node for the app entry (`entry_ipv4`,
      ADR-20261103163954 §2), routed to that server. No role puts it on
      the host: add it to the public card in its own netplan file
      (`/etc/netplan/61-entry-ip.yaml`, `addresses: [<ip>/32]` under the
      card named in `50-cloud-init.yaml`). Check `ip addr show` lists it;
      once the converge has run (nftables + gatewayd-public listening on
      443), check from outside that
      `curl -sk -o /dev/null --connect-timeout 5 -w '%{time_connect}' https://<ip>/`
      connects. Don't use ping: nftables drops ICMP on the public card.
- [ ] Every host whose public DHCP subnet holds another fleet host (on
      2026-11-23: bhs-2 and bhs-3 in 15.235.50.0/24; bhs-1 is alone in
      149.56.23.0/24 and needs nothing) sends that subnet through the
      gateway. This applies to any compute node added later too. OVH
      answers ARP between dedicated servers only sporadically, so the
      on-link route drops replies to the other node's primary address (app
      egress is masqueraded to it, so cross-node `*.gregale.app` calls
      hang). Add `/etc/netplan/62-peer-via-gw.yaml` (mode 600) with, under
      the same card id as `50-cloud-init.yaml`, `routes:` for the two halves
      of the DHCP prefix (prefix length + 1, e.g. `15.235.50.0/25` and
      `15.235.50.128/25` for a /24), each `via:` the DHCP gateway with
      `on-link: true` (the gateway sits inside one half, and at boot the
      static routes are added before DHCP installs its route to the
      gateway). Run `netplan generate`, check that
      `/run/systemd/network/10-netplan-<card>.network` has both routes with
      `GatewayOnLink=true`, and add them live with
      `ip route replace <half> via <gw> dev <card> onlink`. After the next
      planned reboot, check that `ip route` shows both halves. Check with an
      empty neighbour cache, 10 times in each direction (the fault was
      intermittent, so one success proves nothing): on the serving node
      `ip neigh del <caller primary> dev <card>`, then from the caller
      `curl -sk -o /dev/null --connect-timeout 5 --interface <caller primary> -w '%{time_connect}\n' https://<other entry_ipv4>/`
      must print a non-zero time every run. Don't use ping for this check.
      IPv4 only: `*.gregale.app` publishes no AAAA records (2026-11-23).
- [ ] OVH account: 2FA on every login, API tokens scoped and listed, IPMI and
      rescue mode understood (disk encryption, ADR-20261109120000, covers
      the rescue-mode risk to data volumes).
- [ ] OVH Object Storage buckets in the compute region: artifacts,
      log archive, backups. Keys per purpose (below), never shared.
- [ ] **Control-plane state backups run on GCP** (phase 1 step 2). Production
      had none on 2026-11-19 (`faas_cp_state_backup_recipients` was empty),
      and the rebuild cannot keep the CA, the HMAC keys or `sealed.env`
      without a bundle.
- [ ] Each OVH host's private address resolves to its vRack card, not its
      public one (ADR-20261109100000 amendment 2026-11-19): a read-only
      `make ansible-preflight` shows `10.20.0.x` for every host.
- [ ] CD and Ansible reach the OVH hosts from inside the vRack (a runner or
      bastion there) before the host firewall limits SSH to the vRack. CD
      must be able to deploy to the OVH control node in the window. See
      "CD access" below.
- [ ] The OVH manifest (below) passes `make manifest-ansible` and the
      read-only `make ansible-preflight`. Strict `make verify-fleet` runs
      after the window (phase 2 step 9).
- [ ] The restore rehearsal (phase 1 step 9) passed, and its time fits the
      window.
- [ ] No customer bucket outside `deleted`:
      `SELECT state, count(*) FROM object_buckets GROUP BY 1` on GCP.
- [ ] CD can give each OVH compute node its own no-delete key (see "Keys").
      Until it can, the OVH compute nodes join with the shared
      delete-capable key the GCP fleet uses.

## Open blockers (prep pass, 2026-11-20)

Walking phase 2 against the code found these. Each stops the window after
production is down, so all must be closed, and the go/no-go list above is
not complete without them.

1. **No release carries the OVH topology.** Every release signs the
   manifest `deploy/manifest/production/release-template` names (today
   `gcp-live`). The control-plane converge and the compute join use that
   signed manifest, refuse hosts it does not declare and bind its hash, so
   the step 5 deploy and step 6 joins fail with any release so far. Needs a
   release whose pointer is `ovh-live.template.yaml`, ideally GCP's commit
   plus only the pointer and the fixes below (PRs #7710 and #7713), so the
   restored database sees no new migration. CD checks out the release tag,
   so the fixes reach the window only inside that release. Step 12 (the
   pointer PR after the window) is too late.
2. ~~**CD cannot do the first deploy onto a rebuilt control node.**~~
   **Resolved by PR #7713:** `host-state` reports a host with no
   `/opt/faas/current` as fresh, so a full release converges every role.
   The observer then requires `/readyz` to answer ready within 5 minutes
   after the switch, not before it. Repoint `api.` first (rebuild step 9).
3. ~~**gregalectl is not on the host before the first activation.**~~
   **Resolved by PR #7713:** `deploy/scripts/install-release-gregalectl.sh`
   installs GCP's release binary, cosign-verified like CD (rebuild step 2).
4. ~~**The bundle overwrites the OVH backup remote.**~~ **Resolved by PR
   #7713:** rebuild step 4 reinstalls phase 1 step 8's remote right after
   `cp_state_restore.yml`.
5. ~~**Bootstrap needs operator inputs the bundle lacks.**~~ **Resolved by
   PR #7713.**
   - `make … ANSIBLE_VARS_FILE=…` passes a vars file. Rebuild step 6 lists
     a minimal one with no real secrets: key-only root login, a throwaway
     PostgreSQL password, Alertmanager in dev mode and edgednsd off.
   - CD's first activation then converges every role with the real vars.
   - The log archive JSON (`credentials_src`) must be on the operator
     machine and the CD runner host.
   - The Alertmanager receiver files are copied from GCP (rebuild step 8).
   - `secrets init` uses `--preserve-existing --no-db` (rebuild step 5).
6. **Data sealed to GCP's `host.age` would be lost.** `host.age` is not in
   the bundle; `fleet-seal migrate` skips rows it cannot open and MFA TOTP
   envelopes are not migrated. Check on GCP before the freeze.
7. **The xfs role refused the encrypted data volume** (crypto_LUKS):
   fixed by PR #7710, which the OVH release must contain.
8. **Compute joins**: one PKI bundle per node, issued on bhs-1 after the
   restore (`gregalectl pki issue-bundle … --cn bhs-N --transport-san
   bhs-N.gregale.dev`) into `COMPUTE_PKI_TARBALL_B64`, one `cd-compute`
   run per node (`rollout_phase=full`, `pki_source=secret`); the node
   canary goes through public DNS, so skip it (direct `cd-compute`) and run
   acceptance after traffic moves. Stage the Cloudflare tunnel token
   (`/etc/faas/secrets/cloudflared/tunnel-token`) and
   `/etc/faas/secrets/edge-tls/origin-pull-ca.pem` on bhs-2 and bhs-3
   first. `COMPUTE_STORAGE_ENV` must lose the GCS fallback and
   `COMPUTE_ANSIBLE_VARS_B64` must keep key-only root login.
9. **Traffic**: no record exists for `fsn-1`; the owner repoints
   `api.` and `s3.` (proxied) and `origin.` and `edge.` (DNS-only) to
   bhs-1 in the Cloudflare dashboard; app hosts stay on the tunnel;
   `systemctl disable faas-cloudflared` on the GCP nodes, or a reboot
   rejoins the tunnel; custom-domain certificates are issued again on the
   new edge.
10. ~~**Bootstrap side effects.**~~ **Resolved by PR #7713:** rebuild
    step 6 stops the backup timers and every `faas-*` service right after
    bootstrap. Step 7 restarts the timers after the hold. Alertmanager and
    edgednsd stay off until CD's converge. The window plan avoids
    03:00–03:45 UTC.

## Keys

| Key | Bucket | Rights | Where it lives |
|---|---|---|---|
| GCP fleet artifact key (temporary) | artifacts | get, put, list, delete | the `COMPUTE_STORAGE_ENV` secret, phase 1 steps 3–6 until the cutover |
| node artifact key (one per compute node) | artifacts | get, put, list; **no delete** | `storage.env` on that node |
| control-plane artifact key | artifacts | get, put, list, delete | `storage.env` on the control node |
| log archive key | log archive | put, get, list, delete (account erasure) | `log_archive.credentials_src` on the controller |
| backup key | backups | put, get, list; **no delete** | `/etc/faas/secrets/storage-box/rclone.conf` |

The policies are in `deploy/ovh/object-storage-policies/`, with the user
for each key and each bucket's lifecycle. The backup key is append-only
like the GCS writer role: the WAL prune deletes only local segments, and
the bucket lifecycle removes old recovery points (ADR-20261118203000).

Nodes go without delete through `FAAS_STORAGE_DELETE_MODE=defer`: deletes
become `storage_object_deletions` rows that the control plane's object GC
(`FAAS_SCHEDD_OBJECT_GC`) carries out (ADR-20261115180000). The S3 backend
supports it, but CD cannot deliver it yet (checked 2026-11-20):

- every compute rollout copies one `storage.env`, from the
  `COMPUTE_STORAGE_ENV` secret, to every compute node
  (`node_join.yml`) and to the control plane (`node_join_control_plane.yml`),
  so nodes and control plane hold the same key;
- the `gregalectl artifact` commands CD runs (publish, verify,
  lifecycle-check) refuse `FAAS_STORAGE_DELETE_MODE` in that file;
- `artifact lifecycle-check`, which gates every compute and control-plane
  rollout, deletes its probe object, so a no-delete key fails it.

So while the fleet is on GCP it uses one delete-capable key, the user
`gregale-gcp-fleet-artifacts`: the same right the GCP compute and
control-plane service accounts hold on the GCS bucket
(`roles/storage.objectAdmin`). Delete that user at the cutover.

## CD access

CD runs on the control plane: production's two `faas-fleet-us` runners
(`gregale-cp`, `gregale-cp-2`) live on the GCP control node, and they log in
to every host as `root` with one of two keys, `gregale-prod-cd-cp` (the
control plane) and `gregale-prod-cd-compute` (every host). Each host's root
`authorized_keys` limits the key to the runner host's private address
(`from=`, no forwarding), and sshd allows root by key only
(`PermitRootLogin prohibit-password`). The role default is `no`; GCP sets
`prohibit-password` in operator-held Ansible variables.

On OVH the same model points at bhs-1, the control node:

- **Root login (done 2026-11-20).** The three hosts converged
  `host_hardening` with `-e faas_hardening_sshd_permit_root_login=prohibit-password`.
  Pass the same variable to every converge, the window's bootstrap
  included, or it falls back to `no` and locks CD out. Root's
  `authorized_keys` holds only the CD keys, limited to
  `from="10.20.0.1"` (bhs-1's vRack address): bhs-1 has both keys,
  bhs-2 and bhs-3 the compute key. The OVH install's own key for root was
  removed (`/root/.ssh/authorized_keys.bak-20261120` keeps the old file).
- **Runners on bhs-1 (done 2026-11-20).** `make bootstrap-fleet-runner`
  (`deploy/ansible/fleet_runner.yml`) with `faas_runner_hosts=bhs-1`,
  `faas_runner_instances=2` and `faas_runner_labels=["faas-fleet-ovh"]`, a
  label no workflow uses, so the runners (`bhs-1`, `bhs-1-2`, user
  `faas-runner`) are online without taking GCP's jobs. The role accepts
  exactly one `faas-fleet-<word>` label, so `faas-fleet-us-ovh` is refused.
  The Make target fetches a short-lived registration token through `gh`.
- **In the window, before phase 2 step 5's deploy:**
  - stop the GCP runners (`faas-github-runner*.service` on the GCP control
    node) and move the bhs-1 runners from `faas-fleet-ovh` to
    `faas-fleet-us` (`gh api -X PUT
    repos/poyrazK/faas/actions/runners/<id>/labels -f 'labels[]=faas-fleet-us'`
    for each; `gh api repos/poyrazK/faas/actions/runners` lists the ids);
  - set `COMPUTE_KNOWN_HOSTS` to the OVH host keys under the fleet names
    (`fsn-1.gregale.dev`, `bhs-2.gregale.dev`, `bhs-3.gregale.dev`). The
    ED25519 fingerprints on 2026-11-20: bhs-1
    `SHA256:TmZZ4H588uPpzbT7FEOoRsu6qjuaAOLNjrcr1Wjr2QY`, bhs-2
    `SHA256:iqOzBAS7ewNswvawLkqTnXkjJalQ968PCPGuqvi35Jo`, bhs-3
    `SHA256:/I39vLRzzLGSQohnatPFNzLJV1l2oxYuaYFi5ETnjG4`;
  - `CP_HOST` stays `fsn-1.gregale.dev`: bhs-1 resolves the fleet names
    to vRack addresses through the `/etc/hosts` bootstrap renders, so CD's
    traffic never leaves the vRack;
  - `cd-platform` takes the OVH compute nodes in `compute_targets`, with
    those fingerprints in `ssh_host_key_sha256`.

## The OVH manifest

`deploy/manifest/production/ovh-live.template.yaml` is the OVH topology. Its
drift test (`pkg/renderer/ovh_production_manifest_test.go`) keeps it to these
differences from `gcp-live.template.yaml`:

- `fleet`: the OVH hosts. The control-plane entry keeps the GCP `address`
  (`fsn-1.gregale.dev:9091`), so every fleet name and `postgresql.dsn` point
  at the new host with no other edits (`ControlPlaneFailover.md` step 4).
  Compute hosts get `public_ipv4` (their own address) and `entry_ipv4` (the
  Additional IP), and no `public_ipv4_forwarded`.
- `overlay`: `provider: static`, `interface:` the vRack card, `cidr:` the
  vRack range (ADR-20261109100000).
- `ssh.allowed_cidrs`: the vRack range plus operator sources. With no
  provider firewall, nftables is the only SSH boundary.
- `egress`: drop `danger_accept_rfc1918_lateral_movement` and the GCP VPC
  exception; add the vRack range only if needed.
- `disk_encryption`: on, Tang on each host's peers
  (ADR-20261109120000).
- `log_archive`: the OVH endpoint, `auth: static`, `credentials_src`.
- `artifact_signer`: keep enabled (signd on the control node).
- No `postgresql.standby`: the database moves as a backup. A warm standby
  at the other provider (phase 3) needs the manifest to accept a public
  standby address, which it refuses today.

Never put provider IPs other than these host fields, or any key, in the
manifest.

## Phase 1: prepare

1. **Build the OVH hosts.** Install the OS, then converge only what needs no
   fleet: disk encryption, Tang, the host firewall. Do not join the compute
   nodes or bootstrap the control plane.
2. **Turn on control-plane state backups on GCP.** Create an operator age
   identity, keep it offline, and set its public key in
   `faas_cp_state_backup_recipients` for the control plane
   (`ControlPlaneStateBackup.md`, "Turn it on"). After the converge, run
   `faas-cp-state-backup.service` once and check that a bundle lands under
   `offhostbox:faas-cp-state/<GCP control node>/`. The identity is needed
   again only in the rehearsal and the window.
3. **Create the GCP fleet key and prove it.** In the OVH manager, Public
   Cloud, Users & Roles, add the user `gregale-gcp-fleet-artifacts`
   (ObjectStore operator). Then in Object Storage, Users, its `...` menu:
   "Import a user access policy (JSON)" with
   `deploy/ovh/object-storage-policies/gregale-gcp-fleet-artifacts.json`,
   then "Enable". The key id is the row's access key; the secret is under
   "View the secret key".

   Prove it from one GCP compute node with the check CD runs, against a
   throwaway `storage.env` holding the step 4 contents (root:faas 0440 in
   a fresh directory under `/run`, deleted afterwards; the key arrives on
   SSH stdin, not on a command line):

   ```bash
   sudo -u faas-imaged gregalectl artifact lifecycle-check \
     --env-file "$probe/storage.env" --lifecycle-env-file /etc/faas/imaged-storage.env --json
   ```

   It must print `"backend": "s3"`, `"read_verified": true` and
   `"deleted": true`.
4. **Switch the GCP fleet's artifact store to the OVH bucket** with the GCS
   read fallback. Set the `production-us` environment secret
   `COMPUTE_STORAGE_ENV` to today's lines with the backend changed and the
   S3 and fallback lines added (`gh secret set COMPUTE_STORAGE_ENV --env
   production-us` reading stdin):

   ```
   FAAS_STORAGE_BACKEND=s3
   FAAS_STORAGE_LOCAL_PREFIXES=none
   FAAS_REQUIRE_SHARED_ARTIFACTS=1
   FAAS_STORAGE_CACHE_SERVE_STALE=0
   FAAS_STORAGE_SNAPSHOT_COMPRESSION=zstd
   FAAS_STORAGE_FALLBACK_BACKEND=gcs
   FAAS_GCS_BUCKET=gregale-prod-280686321025-artifacts
   FAAS_S3_BUCKET=gregale-prod-artifacts
   FAAS_S3_ENDPOINT=https://s3.bhs.io.cloud.ovh.net
   FAAS_S3_REGION=bhs
   FAAS_S3_PATH_STYLE=0
   FAAS_S3_ACCESS_KEY_ID=…
   FAAS_S3_SECRET_ACCESS_KEY=…
   ```

   Leave out `FAAS_STORAGE_DELETE_MODE` (the artifact commands refuse it),
   and leave `COMPUTE_IMAGED_STORAGE_ENV` as the registry login: an S3 pair
   there overrides this key for imaged, vmmd and apid.

   Then redeploy the release production runs with `cd-platform`: the same
   `release_tag`, `compute_targets` and `compute_pki_source=live-node` as
   the last rollout (the default `secret` is for enrollment and fails on
   existing hosts: "ca.key is missing"). The compute rollout installs the
   file on both compute nodes and the control plane. The control-plane
   stage runs first, so restart the control plane's readers once the
   compute rollout is done:

   ```bash
   sudo systemctl restart faas-apid.service faas-schedd.service faas-signd.service
   ```

   Writes now go to OVH only; a read falls back to GCS only on not-found,
   and a delete hits both. Until a node is switched it cannot read what a
   switched one wrote (a GCS primary has no S3 fallback), so avoid deploys
   while the rollout runs: a wake there cold-boots, and an app built on a
   switched node cannot cold-boot on an unswitched one.

   The first node's canary runs inside that window, so it can fail for
   exactly this reason. On 2026-11-20 fsn-2's canary built on fsn-2 (to
   OVH) and cold-booted on fsn-3 (GCS only): `gcs get ... 404`. The leg
   had already activated fsn-2; only the gate failed, and the rollout
   stopped before fsn-3. Roll forward rather than back: run `cd-platform`
   again with only the remaining node in `compute_targets`. Its canary
   then runs with every node switched (it passed), and the restart of the
   control-plane readers follows.
5. **Bulk-copy the old objects** from the GCP control node, whose service
   account reads the GCS bucket, keeping user metadata (the S3 store reads
   GCS's encoding unchanged), with the step 3 key in the environment only:

   ```bash
   rclone copy --metadata --checksum gcs:<gcs bucket> ovh:<ovh bucket>
   ```

   Repeat until a run copies nothing new. Then do a cross-node restore of a
   snapshot that was written to GCS before step 4, on a node whose cache does
   not hold it. A copy that races a delete can bring a deleted object back
   to OVH, where nothing references it; it costs storage, nothing else.
6. **Remove the fallback**: set `COMPUTE_STORAGE_ENV` again without
   `FAAS_STORAGE_FALLBACK_BACKEND` and `FAAS_GCS_BUCKET`, redeploy and
   restart the control-plane readers as in step 4. From here GCS is read
   by nothing; keep it until phase 3.
7. **Log archive bucket.** Copy `faas-logs/` from the GCS archive to the OVH
   archive bucket (same `rclone copy`) so customers keep their 90 days.
8. **Backup remote on the OVH control node.** Put the OVH backup remote
   (`type = s3`, `no_check_bucket = true`) in
   `/etc/faas/secrets/storage-box/rclone.conf` on the OVH control node under
   the `offhostbox` name, so `offhostbox:faas-pg-basebackup/` is
   `gregale-prod-pg-backups/faas-pg-basebackup/`. The restore reads from it,
   and after the window the backups push to it. `deploy/ovh/install-s3-key.sh
   rclone --ssh '<ssh to the control node>' --bucket gregale-prod-pg-backups`
   writes it without the secret touching a command line. The secret is the
   value under "View the secret key" for the backup user in the OVH manager;
   the manager's "Download rclone file" is a Swift (OpenStack) config and
   holds no S3 key.
9. **Rehearse the restore on the OVH control node.** Copy the newest
   backups to the OVH bucket as in phase 2 step 4 (GCP keeps running). The
   newest base backup is the nightly one (03:00 UTC), and WAL since then
   grows about 1.1 GB an hour, so rehearse early in the UTC day and set the
   WAL `--max-age` just above the backup's age. On
   the OVH control node, restore them into a throwaway cluster ("Restoring
   PostgreSQL", rehearsal): a scratch data directory, another port, no
   archiving and no faas daemon. Then:
   - check that it promoted, that `SELECT max(version_id) FROM
     goose_db_version` matches GCP, and that `accounts` and `apps` row
     counts match GCP within the backup's age;
   - on the operator machine, prove the identity opens the newest state
     bundle without writing its contents anywhere:
     `gcloud storage cat gs://<backup bucket>/faas-cp-state/<GCP control node>/<newest>.tar.age | age -d -i cp-state-operator.key | tar -tf -`;
   - time the copy and the restore;
   - stop the throwaway cluster and delete its data directory and the staged
     backup.

   The 2026-11-19 rehearsal on the OVH control node restored the 03:05 UTC
   base backup: fetching it (826 MB) took 47 s and unpacking it (3.4 GB)
   13 s, and the database was consistent 13 s after start. Replaying
   875 WAL segments (14 GB, up to 17:32 UTC) took 16 min 29 s, about one
   segment a second. That rate is fetching, not replay (PostgreSQL used 8 s of CPU):
   `restore_command` starts one `rclone` per segment. It promoted at
   migration `20261119102834810`, the same as GCP, with 14 accounts and
   744 apps, the same as GCP's rows created by the last replayed
   transaction.
10. **Build the control plane's slow software ahead**, on the OVH control
    node, with the release GCP will run at the freeze, and again the day
    before the window if a release changed its pins. It is the
    pre-install play of `deploy/ansible/postgres_standby.yml` pointed at
    the OVH hosts: `firecracker` builds the guest kernel from source and
    fetches Firecracker, `supply_chain_scanners` fetches Syft, Grype and the
    Grype database. Both only download, verify and build: no service
    starts, no `/etc/faas` file is read and nothing touches a database, so
    they are safe before the restore. The window's bootstrap then finds
    their output current. Render the OVH manifest and inventory with the
    release's pins (`scripts/materialize-release-manifest.sh` with the
    release's `production-manifest.yaml` values and `runtime-bases.env`,
    then `gregalectl manifest ansible`), and run:

    ```yaml
    - hosts: bhs-1                 # faas_box_role: control-plane
      gather_facts: false
      become: true
      roles: [firecracker, supply_chain_scanners]
    - hosts: "bhs-2:bhs-3"         # compute-only
      gather_facts: false
      become: true
      roles: [supply_chain_scanners]
    ```

    On 2026-11-20 with rc.360 it took 10 min 51 s (vmlinux 4 min 34 s on
    4 jobs, Grype database 1–3 min per host), and a second run 82 s. Then
    run the read-only `make ansible-preflight` with the same inventory.
11. **Lower the TTLs** of the control-plane address record and the app entry
    records to 60 s, a day ahead.

## Phase 2: cut over (one window)

1. **Freeze**: pause CD, announce the window, no deploys. Note the release
   GCP's control plane runs (commit and any per-unit versions): OVH must get
   that release, not a newer one, because the restored database is at its
   migrations.
2. **Stop GCP's control plane.** On the GCP control node, stop every faas
   unit and timer, and leave PostgreSQL up. The API is down from here.

   ```bash
   sudo systemctl stop 'faas-*.timer' 'faas-*.service'
   sudo -u postgres psql -d faas -Atc \
     "SELECT (SELECT max(version_id) FROM goose_db_version), (SELECT count(*) FROM accounts), (SELECT count(*) FROM apps)"
   ```

   Keep that output: step 5 compares the restored database with it.
3. **Take the final backups**, one after the other, then stop PostgreSQL:

   ```bash
   sudo systemctl start faas-pg-basebackup.service
   sudo systemctl start faas-pg-basebackup-push.service
   sudo systemctl start faas-cp-state-backup.service
   sudo -u postgres psql -Atc 'SELECT pg_walfile_name(pg_switch_wal())'
   sudo -u postgres psql -Atc 'SELECT last_archived_wal, failed_count FROM pg_stat_archiver'
   ```

   Repeat the last query until `last_archived_wal` is the segment the switch
   printed. Check each unit's journal for success. Then stop PostgreSQL and
   mask it, so nothing on GCP can write again (the cluster is `16 main`;
   check with `pg_lsclusters`):

   ```bash
   sudo systemctl stop postgresql@16-main
   sudo systemctl mask postgresql@16-main
   ```

4. **Copy the backups to OVH.** In a root shell on the GCP control node
   (`sudo -i`), give rclone the OVH backup key through the environment, for
   this shell only, so it is never written to disk:

   ```bash
   read -r -p 'OVH backup key id: ' RCLONE_CONFIG_OVH_ACCESS_KEY_ID
   read -r -s -p 'OVH backup key secret: ' RCLONE_CONFIG_OVH_SECRET_ACCESS_KEY; echo
   export RCLONE_CONFIG_OVH_ACCESS_KEY_ID RCLONE_CONFIG_OVH_SECRET_ACCESS_KEY \
     RCLONE_CONFIG_OVH_TYPE=s3 RCLONE_CONFIG_OVH_PROVIDER=Other \
     RCLONE_CONFIG_OVH_ENDPOINT=https://s3.bhs.io.cloud.ovh.net \
     RCLONE_CONFIG_OVH_REGION=bhs RCLONE_CONFIG_OVH_NO_CHECK_BUCKET=true
   conf=/etc/faas/secrets/storage-box/rclone.conf
   rc=/usr/local/lib/faas/faas-rclone-backup-identity.py
   dst=ovh:gregale-prod-pg-backups
   b=$("$rc" --config "$conf" lsf --dirs-only offhostbox:faas-pg-basebackup/ | sort | tail -1)
   "$rc" --config "$conf" copy --checksum "offhostbox:faas-pg-basebackup/$b" "$dst/faas-pg-basebackup/$b"
   "$rc" --config "$conf" copy --checksum --transfers 16 --max-age 2h offhostbox:faas-pg-wal "$dst/faas-pg-wal"
   "$rc" --config "$conf" copy --checksum --max-age 2h offhostbox:faas-cp-state "$dst/faas-cp-state"
   exit
   ```

   Run rclone through `faas-rclone-backup-identity.py`, as the backup units
   do. The control node's own service account cannot list the backup bucket
   (plain `rclone` gets 403 `storage.objects.list`); the wrapper uses it only
   to mint a one-hour token for the backup writer, and passes the `ovh`
   remote through unchanged. The rehearsal (2026-11-19) copied the base
   backup in 41 s and 15 GB of WAL in 67 s, well inside the token's hour.
   Do not skip the fresh base backup of step 3 to save time: the restore
   fetches WAL one segment a second, so a night's WAL adds about 15 minutes
   to the window.

   `$b` must be the backup step 3 made. Recovery needs WAL only from that
   backup's start, so copy no older WAL than the backup: production writes
   about 1.1 GB of WAL an hour (27 GB a day, 2026-11-19), against a base
   backup of about 0.8 GB. Widen `--max-age` if step 3 started more than an
   hour before this copy. The backup key can write but not delete, so a
   repeated copy is safe.
5. **Rebuild the control plane on OVH** with the OVH manifest, following
   `ControlPlaneStateBackup.md`, "Rebuild a control plane from backups",
   steps 1–9 (step 10 comes after the joins). For this move:
   - step 1: the manifest is `ovh-live.template.yaml`, materialized with the
     release from step 1;
   - step 2: install the `gregalectl` of the release GCP's control plane
     runs;
   - step 3: the bundle is in the OVH bucket under
     `faas-cp-state/<GCP control node>/`;
   - step 4: reinstall phase 1 step 8's remote (`install-s3-key.sh rclone
     … --bucket gregale-prod-pg-backups`), because the bundle brought back
     GCP's;
   - step 6: bootstrap from a checkout of the release from step 1. The
     manifest's `log_archive.credentials_src`
     (`/operator/secrets/log-archive-s3.json`) must exist on the operator
     machine and on the CD runner host;
   - step 7: restore as in "Restoring PostgreSQL" below, and compare it
     with step 2's output before the object-storage hold;
   - step 8: GCP's bundles lack the Alertmanager receiver files, so copy
     them from the GCP control node (`ssh <old host>` is `gcloud compute ssh
     <GCP control node> --zone us-east1-b --tunnel-through-iap --command`);
   - step 9: the owner repoints `api.` (and `s3.`, `origin.`, `edge.`) to
     the OVH control node in Cloudflare first. Then deploy the release from
     step 1 with `CP_HOST` set to the OVH control node.
6. **Join the OVH compute nodes** (`provider-neutral-node-join.md`) with the
   node keys and `FAAS_STORAGE_DELETE_MODE=defer`. Run the serving probe on
   each.
7. **Retire the GCP compute nodes**, one at a time. They cannot reach the
   new control plane, so nothing parks: on each, stop every faas unit,
   including the tunnel connector (`faas-cloudflared`), so it stops taking
   traffic:

   ```bash
   sudo systemctl stop 'faas-*.timer' 'faas-*.service'
   ```

   Then, against the OVH control plane, cold-evict its instances and retire
   it (`compute-node-compromise.md` uses the same commands):

   ```bash
   gregalectl compute-nodes force-drain --node <gcp node> --reason move_to_ovh --yes
   gregalectl compute-nodes drain-status --node <gcp node>   # until empty
   gregalectl compute-nodes retire --node <gcp node> --reason move_to_ovh --yes
   ```

   Their apps wake on OVH on the next request, from the app layers in the OVH
   bucket. The warm apps they served are down from the stop until that wake.
   Keep the machines until phase 3: they are the rollback.
8. **Move app traffic**: point the app entry records (or the tunnel, if the
   zone still uses it) at the OVH compute nodes (`EdgeDirectEntry.md`,
   `AppEdgeCutover.md`), and the platform hosts' Cloudflare origin at the OVH
   control node (`ControlPlaneFailover.md` step 9).
9. **Check**: `make verify-fleet`, the API, one deploy, one cold wake, one
   custom domain, and that the newest change made before the freeze is
   present. There is no customer bucket to read until an OVH object
   storage backend exists (phase 3).
10. **Warm the busiest apps**: request each app that served traffic in the
    last day, a few at a time, so their one cold boot happens under watch
    and not on a customer's first request. Watch node memory and the
    cold-boot failure rate.
11. **Backups**: run the immediate push (`PostgresBackup.md`) and
    `faas-cp-state-backup.service`, and confirm both land in the OVH backup
    bucket.
12. **Point releases at OVH**: merge the PR that changes
    `deploy/manifest/production/release-template` to `ovh-live.template.yaml`
    before the first release after the window. Until it merges, a release
    still materializes the GCP topology.
13. **Resume CD.**

### Restoring PostgreSQL

The backup is `pg_basebackup -Ft -z -X stream` (`faas-pg-basebackup.service`):
`base.tar.gz` is the data directory and `pg_wal.tar.gz` the WAL streamed
during the backup, which belongs in `pg_wal`. With `recovery.signal` and no
recovery target, PostgreSQL replays every archived segment from
`faas-pg-wal/`, then promotes onto a new timeline. `offhostbox` is the OVH
backup bucket (phase 1 step 8). Run as root on the OVH control node.

Fetch and unpack the newest backup into `$data`:

```bash
conf=/etc/faas/secrets/storage-box/rclone.conf
b=$(rclone --config "$conf" lsf --dirs-only offhostbox:faas-pg-basebackup/ | sort | tail -1)
stage=/srv/fc/pg-restore/$b
rclone --config "$conf" copy --checksum "offhostbox:faas-pg-basebackup/$b" "$stage"
install -d -o postgres -g postgres -m 0700 "$data" "$data/pg_wal"
tar -xzf "$stage/base.tar.gz" -C "$data"
tar -xzf "$stage/pg_wal.tar.gz" -C "$data/pg_wal"
chown -R postgres:postgres "$data"
touch "$data/recovery.signal"
```

**In the window**, `$data` is the cluster bootstrap made. Move its empty
data directory aside first, and give recovery its WAL source through
`conf.d`. The cluster's own configuration under `/etc` stays, including its
`archive_command`, so the promoted timeline's WAL goes to the OVH bucket:

```bash
data=$(pg_lsclusters -h | awk '$1 == 16 && $2 == "main" {print $6}')
systemctl stop postgresql@16-main
mv "$data" "$data.bootstrap"     # delete once the restore checks out
# fetch and unpack, as above
cat > /etc/postgresql/16/main/conf.d/95-faas-restore.conf <<'CONF'
restore_command = 'rclone copyto offhostbox:faas-pg-wal/%f %p --config /var/lib/pgsql/backup-rclone.conf --stats=0 --quiet'
CONF
systemctl start postgresql@16-main
sudo -u postgres psql -Atc 'SELECT pg_is_in_recovery()'   # repeat until f
rm /etc/postgresql/16/main/conf.d/95-faas-restore.conf
systemctl reload postgresql@16-main
```

**In the rehearsal**, `$data` is a scratch directory such as
`/srv/fc/pg-rehearsal`, and nothing about the real cluster changes. The
backup carries no `postgresql.conf` or `pg_hba.conf` (they live under
`/etc`), so write minimal ones. Leave archiving off: a promoted throwaway
that archives puts its new timeline in `faas-pg-wal/`, where the backup key
cannot delete it and the real restore would follow it. Recovery refuses
settings lower than the primary's, and `pg_controldata` reads those from
the backup. Bootstrap has not run yet, so install `postgresql-16` and
`rclone` from the Ubuntu archive first (the package starts an empty
`16 main` cluster; bootstrap configures it later), and give PostgreSQL a
copy of the remote it can read:

```bash
data=/srv/fc/pg-rehearsal
install -o root -g postgres -m 0440 "$conf" /srv/fc/pg-rehearsal-rclone.conf
# fetch and unpack, as above
/usr/lib/postgresql/16/bin/pg_controldata "$data" | grep 'setting:'
cat > "$data/postgresql.conf" <<'CONF'
port = 5433
listen_addresses = ''
unix_socket_directories = '/tmp'
archive_mode = off
restore_command = 'rclone copyto offhostbox:faas-pg-wal/%f %p --config /srv/fc/pg-rehearsal-rclone.conf --stats=0 --quiet'
# max_connections, max_worker_processes, max_wal_senders,
# max_prepared_transactions, max_locks_per_transaction: at least the
# values pg_controldata printed
CONF
printf 'local all postgres peer\n' > "$data/pg_hba.conf"
chown postgres:postgres "$data/postgresql.conf" "$data/pg_hba.conf"
runuser -u postgres -- /usr/lib/postgresql/16/bin/pg_ctl -D "$data" -l "$data/rehearsal.log" -W start
runuser -u postgres -- /usr/lib/postgresql/16/bin/pg_ctl -D "$data" status   # must say running
runuser -u postgres -- psql -h /tmp -p 5433 -Atc 'SELECT pg_is_in_recovery()'   # repeat until f
```

The log goes inside `$data`: `postgres` cannot create files in `/srv/fc`,
and with `-W` a log it cannot open means no server and no error, only a
`psql` that never connects. Check `status` before waiting on recovery.

After the checks in phase 1 step 9, stop it
(`pg_ctl -D "$data" -m fast stop` as `postgres`) and delete `$data`,
`/srv/fc/pg-rehearsal-rclone.conf` and the staged backup.

### Rollback

- **Before the OVH control plane serves traffic** (up to phase 2 step 8):
  1. stop every faas unit on the OVH control node and on any OVH compute
     node step 6 joined (their tunnel connectors would take traffic), so
     only one fleet runs;
  2. on the GCP control node, `systemctl unmask postgresql@16-main`, start
     PostgreSQL, then start the faas units and timers (or run the
     control-plane deploy for the same release). GCP's database has not
     changed since the freeze;
  3. start the faas units on any GCP compute node step 7 stopped. The
     force-drain and retire in step 7 were written to the OVH database
     only.
- **After OVH served traffic**, writes exist only on OVH. Going back is
  another move in the other direction (this runbook with the providers
  swapped), not a restart.

## Phase 3: settle

- **Delete the GCP machines** after a week without rollback. Delete the GCS
  artifact bucket only after a full `gregalectl artifact lifecycle-check` and
  a month of backups on OVH.
- **Warm standby at GCP (ADR-1344 §8)**: optional and later. A replica at
  another provider reaches the primary over the internet, which the
  manifest's `postgresql.standby` refuses today (its address must be inside
  `overlay.cidr`). It needs a cross-site option first (TLS replication
  already verifies the primary), then `ControlPlaneFailover.md` step 10.
- **Customer object storage on OVH** (set up 2026-11-21): project
  `gregale-customer-storage-us-1`, users and policies in
  `deploy/ovh/customer-object-storage-policies/`, backend `ovh-bhs-prod-v1`
  (`driver: s3`, `versioning: unsupported` (ADR-20261121150000),
  `max_buckets: 95` until OVH raises the project quota to 1,000) as the
  `us-east-1` default, and the `object_storage` block in
  `ovh-live.template.yaml`. The gateway runs on the control plane behind
  Cloudflare. Launch runs `gateway_safety_v1` accounting, which needs no
  OVH usage driver, and no Object Lock (no `object_lock` in the registry)
  until creation-time Object Lock (ADR-20261119065000) ships. The 11-19
  restore brought back the seven deleted GCS test buckets as `ready`
  catalog rows with no upstream bucket; they are retired in the catalog
  before `s3_enabled` turns on.
- **Billing**: the egress allowance numbers were set for GCP egress costs
  (billing review R8-1); revisit them now.
- **Managed PostgreSQL**: the provider registry (`FAAS_MANAGED_POSTGRES_CONFIG`)
  was hand-installed on the GCP control plane and did not move: after the
  2026-11 cutover every `gregale postgres` command answered "Managed
  PostgreSQL is not configured for this environment". Set
  `faas_managed_postgres_config` with the backends the databases were created
  on and converge the control plane; the `managed_postgres_config` role
  refuses a config under which existing databases would not resolve
  (`deploy/ansible/roles/managed_postgres_config/README.md`). Check
  `gregale postgres list` afterwards.

## Record

For the rehearsal and the real run: API downtime, each step's duration, the
`rclone` totals, the restore time, cold-wake p50 on OVH, and anything
bootstrap asked for that this runbook misses.
