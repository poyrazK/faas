# Moving production from GCP to OVHcloud bare metal (ADR-1344 §8)

This runbook moves the whole fleet: the shared artifact store, the control
plane, the compute nodes, app traffic, the log archive and the off-host
backups. It strings together runbooks that already exist and adds the steps
only this move needs. Read each linked runbook before the day.

The plan has three phases:

1. **Prepare (days ahead, no customer impact).** The OVH hosts are built and
   joined to nothing. The GCP fleet switches its artifact store to the OVH
   bucket, and the old GCS objects are copied over.
2. **Cut over (one window).** The OVH control node is promoted, OVH compute
   nodes join, GCP compute nodes drain, and app DNS moves.
3. **Settle (days after).** GCP becomes the warm standby or is retired.

Expect about 15 minutes of API downtime (promotion to first deploy) and one
cold boot per app on its first OVH wake: GCP production runs without a CPU
template, so its snapshots belong to the GCP platform's slot and no OVH CPU
restores them (ADR-20261024225612, `cpu_incompatible` in the cold-reason
metric). Each app is recaptured on OVH after that boot.

## Why this order

- **No phase runs OVH and GCP hosts against each other over the internet.**
  Compute nodes reach the control plane's Postgres with `sslmode=disable`
  and schedd/vmmd over the private overlay. That is fine on a VPC or a
  vRack, and not across providers. The only cross-provider link is the
  Postgres replica, which is TLS with a pinned certificate
  (`FaasPostgresStandby.md`).
- **OVH hosts cannot read GCS.** There is no metadata server off GCP, and
  the `gregale-prod` org policy (`iam.disableServiceAccountKeyCreation`)
  forbids the service-account key a `GOOGLE_APPLICATION_CREDENTIALS`
  fallback would need. So the store moves while the fleet is still on GCP:
  GCP nodes write to the OVH bucket and read misses through their own GCS
  identity (`FAAS_STORAGE_FALLBACK_BACKEND=gcs`).
- **Drained apps park to the shared store.** Once every artifact lives in the
  OVH bucket, a GCP node can drain and its apps wake on OVH from their app
  layers (a cold boot, see above) with nothing left on GCP.

## Go / no-go (all must hold before phase 1)

- [ ] S3 artifact store (PR #6724, ADR-20261107123000) merged and in the
      release GCP runs.
- [ ] OVH hardware delivered: one control node (≥ 32 GB RAM, NVMe) and the
      compute nodes, all on one vRack. **Compute CPUs are Ice Lake or Milan**:
      Firecracker CPU templates (`pkg/fcvm/cputemplate.go`) cover Skylake,
      Cascade Lake, Ice Lake and Milan only, and a snapshot restores only on
      its own CPU class. Naples (EPYC 7xx1) and Ryzen are out. Buy one CPU
      model for all compute nodes, or set `vmmd_cpu_template` (T2CL for
      Cascade/Ice Lake, T2A for Milan) so snapshots move between them.
- [ ] The cold-boot memory headroom fix (#7440, ADR-20261116170000) is in
      the release: every app cold-boots once on cutover day, and 128 MB apps
      ran out of memory on cold boot without it.
- [ ] One OVH Additional IP per compute node for the app entry (`entry_ipv4`,
      ADR-20261103163954 §2).
- [ ] OVH account: 2FA on every login, API tokens scoped and listed, IPMI and
      rescue mode understood (disk encryption, ADR-20261109120000, covers
      the rescue-mode risk to data volumes).
- [ ] OVH Object Storage buckets in the compute region: artifacts,
      log archive, backups. Keys per purpose (below), never shared.
- [ ] The OVH manifest (below) passes `make manifest-ansible` and strict
      `verify-fleet` in a dry run.
- [ ] `ControlPlaneFailover.md` drilled once on staging hosts.

## Keys

| Key | Bucket | Rights | Where it lives |
|---|---|---|---|
| node artifact key (one per compute node) | artifacts | get, put, list; **no delete** | `storage.env` on that node |
| control-plane artifact key | artifacts | get, put, list, delete | `storage.env` on the control node |
| log archive key | log archive | put, get, list | `log_archive.credentials_src` on the controller |
| backup key | backups | put, get, list, delete (WAL prune) | `/etc/faas/secrets/storage-box/rclone.conf` |

Nodes go without delete through `FAAS_STORAGE_DELETE_MODE=defer`: deletes
become `storage_object_deletions` rows that the control plane's object GC
(`FAAS_SCHEDD_OBJECT_GC`) carries out (ADR-20261115180000). The S3 backend
supports it.

## The OVH manifest

Copy `deploy/manifest/production/gcp-live.template.yaml` and change only:

- `fleet`: the OVH hosts. Compute hosts get `public_ipv4` (their own address)
  and `entry_ipv4` (the Additional IP); drop `public_ipv4_forwarded`.
- `overlay`: `provider: static`, `interface:` the vRack card, `cidr:` the
  vRack range (ADR-20261109100000).
- `ssh.allowed_cidrs`: the vRack range plus operator sources. With no
  provider firewall, nftables is the only SSH boundary.
- `egress`: drop `danger_accept_rfc1918_lateral_movement` and the GCP VPC
  exception; add the vRack range only if needed.
- `disk_encryption`: on, Tang on each host's peers
  (ADR-20261109120000).
- `log_archive`: the OVH endpoint, `auth: static`, `credentials_src`.
- `postgresql.standby`: during phase 1, the OVH control node (it starts as
  the GCP primary's replica).
- `artifact_signer`: keep enabled (signd on the control node).

Never put provider IPs other than these host fields, or any key, in the
manifest.

## Phase 1: prepare

1. **Build the OVH hosts.** Install the OS, then converge only what needs no
   fleet: disk encryption, Tang, the host firewall. Do not join compute
   nodes yet.
2. **Seed the OVH control node as the replica** of the GCP primary
   (`FaasPostgresStandby.md`; `make bootstrap-postgres-standby`). The GCP
   firewall admits 5432 from the OVH control node's address only. Wait for
   `FaasPostgresStandby*` to go quiet. This also stages the control plane's
   host files on it (`ControlPlaneFailover.md`, "Before you need it").
3. **Prove the bucket** from one GCP compute node with the node key:

   ```bash
   sudo gregalectl artifact lifecycle-check   # with the S3 storage.env override
   ```

4. **Switch the GCP fleet's artifact store to the OVH bucket** with the GCS
   read fallback, one node at a time (drain, change `storage.env`, restart,
   undrain):

   ```
   FAAS_STORAGE_BACKEND=s3
   FAAS_S3_BUCKET=… FAAS_S3_ENDPOINT=https://s3.<region>.io.cloud.ovh.<tld>
   FAAS_S3_REGION=… FAAS_S3_PATH_STYLE=…
   FAAS_S3_ACCESS_KEY_ID=… FAAS_S3_SECRET_ACCESS_KEY=…
   FAAS_STORAGE_FALLBACK_BACKEND=gcs
   ```

   The control plane switches the same way, with its own key. New builds and
   snapshots now land in OVH; misses read GCS.
5. **Bulk-copy the old objects** from a GCP host, keeping user metadata (the
   S3 store reads GCS's encoding unchanged):

   ```bash
   rclone copy --metadata --checksum gcs:<gcs bucket> ovh:<ovh bucket>
   ```

   Repeat until a run copies nothing new. Then do a cross-node restore of a
   snapshot that was written to GCS before step 4, on a node whose cache does
   not hold it.
6. **Remove the fallback** (`FAAS_STORAGE_FALLBACK_BACKEND` unset) node by
   node. From here GCS is read by nothing; keep it until phase 3.
7. **Log archive bucket.** Copy `faas-logs/` from the GCS archive to the OVH
   archive bucket (same `rclone copy`) so customers keep their 90 days.
8. **Backups.** Put the OVH backup remote (`type = s3`) in
   `/etc/faas/secrets/storage-box/rclone.conf` on the OVH control node and
   name it in `off_host_backup_remote`. The backup wrapper passes non-GCS
   remotes through unchanged. Its first push happens after promotion.
9. **Lower the TTLs** of the control-plane address record and the app entry
   records to 60 s, a day ahead.

## Phase 2: cut over (one window)

1. **Freeze**: pause CD, announce the window, no deploys.
2. **Control plane**: `ControlPlaneFailover.md`, planned switchover steps
   1–7, with the OVH manifest from step 4 on. The API is down from step 2
   until step 7.
3. **Join the OVH compute nodes** (`provider-neutral-node-join.md`) with the
   node keys and `FAAS_STORAGE_DELETE_MODE=defer`. Run the serving probe on
   each.
4. **Drain the GCP compute nodes**, one at a time:

   ```bash
   gregalectl compute-nodes drain --node <gcp node> --reason 'move to OVH'
   ```

   Apps park to snapshots in the OVH bucket and wake on OVH on their next
   request. Then stop the GCP compute daemons so nothing on GCP connects to
   the new control plane.
5. **Move app traffic**: point the app entry records (or the tunnel, if the
   zone still uses it) at the OVH compute nodes (`EdgeDirectEntry.md`,
   `AppEdgeCutover.md`), and the platform hosts' Cloudflare origin at the OVH
   control node (planned switchover step 9).
6. **Warm the busiest apps**: request each app that served traffic in the last
   day, a few at a time, so their one cold boot happens under watch and not
   on a customer's first request. Watch node memory and the cold-boot
   failure rate.
7. **Check**: `make verify-fleet`, the API, one deploy, one cold wake, one
   custom domain, a bucket read through the S3 gateway, and that a commit made
   before the promotion is present.
8. **Backups**: run the immediate push (`PostgresBackup.md`) and confirm it
   lands in the OVH backup bucket.
9. **Point releases at OVH**: merge the PR that changes
   `deploy/manifest/production/release-template` to `ovh-live.template.yaml`
   before the first release after the window. Until it merges, a release
   still materializes the GCP topology.
10. **Resume CD.**

### Rollback

- Before phase 2 step 3: reverse the switchover (the GCP host becomes the
  replica of OVH, then is promoted back). The artifact store is already on
  OVH and both sides can read it.
- After GCP compute drained: undrain the GCP nodes, then reverse the
  switchover. GCP nodes still hold S3 keys, so they read the same store.

## Phase 3: settle

- **Warm standby on GCP (ADR-1344 §8)**: rebuild the old GCP primary as the
  OVH primary's replica (planned switchover step 10). Run the failover drill
  in that direction.
- **Retire GCP compute** after a week without rollback. Delete the GCS
  artifact bucket only after a full `gregalectl artifact lifecycle-check` and
  a month of backups on OVH.
- **Billing**: the egress allowance numbers were set for GCP egress costs
  (billing review R8-1); revisit them now.

## Record

For the drill and the real run: API downtime, each step's duration, the
`rclone` totals, cold-wake p50 on OVH, and anything bootstrap asked for that
this runbook misses.
