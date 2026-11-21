# Control-plane state backup (ADR-1344 §8)

The control plane holds host-local state that is neither in PostgreSQL nor in
the release bundle. The list is in `deploy/ansible/vars/cp_state_files.yml`:
- `sealed.env` and the other `/etc/faas` env files;
- `/etc/faas/secrets`, except the per-host `host.age`;
- `/etc/faas/tls`, including the internal CA key;
- the apid audit and recovery HMAC keys;
- the Caddy origin certificate.

Losing it with the host means:
- re-issuing every node's certificates;
- invalidating every MFA recovery code;
- breaking audit joins;
- re-provisioning every secret.

Two copies protect it:
- **The warm standby** stages it on every converge
  (`docs/runbooks/ControlPlaneFailover.md`).
- **`faas-cp-state-backup`** runs daily on the control plane. It seals the
  same list into an age-encrypted bundle and pushes it to the PostgreSQL
  backups' rclone remote at
  `offhostbox:faas-cp-state/<host>/cp-state-<UTC time>.tar.age`. It verifies
  the remote copy before keeping the last 3 locally.

The bundle is encrypted only to operator age recipients. The control plane
cannot read its own backup, and the identity that can read it must never live
on the control plane.

## Turn it on

1. On an operator machine, create an identity and keep it offline, for
   example in the password manager:

   ```bash
   age-keygen -o cp-state-operator.key   # prints the public key: age1...
   ```

2. Set the public key or keys (more than one operator is fine) in the
   control plane's Ansible vars:

   ```yaml
   faas_cp_state_backup_recipients:
     - age1...
   ```

   Production sets them in
   `deploy/ansible/group_vars/control_plane/cp_state_backup.yml` (since
   2026-11-19). The matching identity is held offline by the owner.

3. Converge the control plane. The role installs
   `/etc/faas/cp-state-backup.{paths,recipients}` and starts
   `faas-cp-state-backup.timer` (daily 04:15 UTC). To run it once now:
   `systemctl start faas-cp-state-backup.service`.

4. Check the result:

   ```bash
   journalctl -u faas-cp-state-backup.service --since -1h
   cat /var/lib/node_exporter/textfile_collector/faas_cp_state_backup.prom
   ```

To add paths outside the list, set `faas_cp_state_extra_files`. Both the
backup and the standby take them. Production adds its Alertmanager receiver
files there, which live outside `/etc/faas`.

## Alerts

| Alert | Meaning | Act |
|---|---|---|
| `FaasControlPlaneStateBackupFailed` | The unit failed. | Read the journal. Usual causes: the rclone remote is missing or unreachable (`/etc/faas/secrets/storage-box/rclone.conf`), the installed `gregalectl` predates `backup seal-cp-state`, or a state file over 16 MB. |
| `FaasControlPlaneStateBackupStale` | No verified push for 26 hours. | Same checks. Also check that the timer is enabled (`systemctl list-timers faas-cp-state-backup.timer`). |

## Rebuild a control plane from backups

Use this when both the control plane and the standby are lost. With a
healthy standby, fail over instead.

Plan the window so that steps 6 and 7 finish before 03:00 UTC or start
after 03:45 UTC. Bootstrap enables the nightly PostgreSQL basebackup
(03:00 UTC) and push (03:30 UTC) timers, each with up to 15 minutes'
random delay. A push of the still-empty cluster becomes the newest
basebackup on the remote, and the restore in step 7 would pick it.

1. Provision the replacement host and render a manifest whose control-plane
   entry names it, keeping the old `address`
   (`docs/runbooks/ControlPlaneFailover.md`, step 4).
2. Install the old control plane's `gregalectl`. Steps 3, 5 and 6 need it,
   and normally only a release activation installs it. The script checks
   the release exactly as `cd-controlplane` does (SHA256SUMS, then cosign
   against the release workflow's identity for that tag), and it refuses a
   host that already has an active release:

   ```bash
   ssh <admin>@<new host> 'sudo bash -s -- <old release tag>' < deploy/scripts/install-release-gregalectl.sh
   ```

3. Copy the newest bundle from `offhostbox:faas-cp-state/<old host>/` to the
   new host. Then, with the operator identity, which you bring for this step
   only and then delete:

   ```bash
   gregalectl backup restore-cp-state --identity cp-state-operator.key \
     --in cp-state-<time>.tar.age --stage /var/lib/faas-standby
   shred -u cp-state-operator.key
   ```

4. Install the files **before** the first bootstrap, so bootstrap keeps the
   restored CA and HMAC keys instead of generating new ones:

   ```bash
   ANSIBLE_CONFIG=deploy/ansible/ansible.cfg ansible-playbook \
     -i deploy/ansible/.generated/inventory/hosts.ini deploy/ansible/cp_state_restore.yml
   ```

   The bundle also installed the old host's backup remote
   (`/etc/faas/secrets/storage-box/rclone.conf`). When the new host keeps
   its backups elsewhere, as in a provider move, install its own remote now.
   Step 7 restores from this remote, so it must reach the bucket that holds
   the old host's basebackups and WAL. For OVH Object Storage:

   ```bash
   deploy/ovh/install-s3-key.sh rclone --ssh 'ssh <admin>@<new host>' --bucket <backup bucket>
   ```

5. Give the new host its own `host.age`. A plain `gregalectl secrets init`
   refuses, because the restored secrets are there. `--preserve-existing`
   creates only the missing files. `--no-db` skips the database write,
   since PostgreSQL is not up yet:

   ```bash
   sudo gregalectl secrets init --preserve-existing --no-db
   ```

   Then re-seal the values sealed outside PostgreSQL for it
   (`docs/runbooks/FaasFleetSealDomain.md`).
6. Bootstrap from a checkout of the release you will deploy in step 9.
   Bootstrap normally gets its inputs from CD's `CP_ANSIBLE_VARS_B64`. This
   run needs only these, in a vars file on the operator machine (mode
   0600, deleted after step 9):

   ```yaml
   # CD converges and activates as root with a key. The role default "no"
   # would lock it out.
   faas_hardening_sshd_permit_root_login: prohibit-password
   # A throwaway (for example `openssl rand -hex 24`). Step 7 replaces this
   # cluster and its roles, and CD's converge in step 9 sets the real one.
   faas_postgres_password: "<throwaway>"
   # Keep Alertmanager silent and edgednsd off until step 9 converges them
   # with the real receivers and DNS token.
   am_dev_mode: true
   faas_edgednsd_enabled: false
   ```

   If the manifest's `log_archive` uses `credentials_src`, that file must
   exist on the machine that runs Ansible. For OVH, create it with
   `deploy/ovh/install-s3-key.sh log-archive --out <credentials_src>`. CD's
   runner needs the same file.

   ```bash
   make ANSIBLE_INVENTORY=deploy/ansible/.generated/inventory/hosts.ini \
     ANSIBLE_VARS_FILE=<vars file> bootstrap-control-plane
   ```

   Then stop what bootstrap started, so nothing runs against the empty
   cluster or the restored one before step 7's hold:

   ```bash
   sudo systemctl stop faas-pg-basebackup.timer faas-pg-basebackup-push.timer faas-cp-state-backup.timer
   sudo systemctl stop 'faas-*.service'
   ```

7. Restore PostgreSQL from the off-host basebackups
   (`docs/runbooks/PostgresBackup.md`). Before any daemon starts against it,
   turn S3 off in the database itself, then turn the backup timers back on:

   ```bash
   sudo gregalectl object-storage hold --db-env /etc/faas/sealed.env
   sudo systemctl start faas-pg-basebackup.timer faas-pg-basebackup-push.timer faas-cp-state-backup.timer
   ```

   `s3_enabled` lives in the database, so the restore brought it back as it
   was at the restore point, usually on. With it came back every S3 key,
   copy source, compute binding and access grant revoked after that point.
   The hold needs no apid.

   The restore also promoted the database onto a new PostgreSQL timeline.
   That alone fences the S3 gateway and public reads (ADR-20261025111015
   amendment 1); the hold covers a backup taken before that fence existed.
   S3 requests and public bucket reads answer 503 until step 10 is done.
8. Put back what the bundle lacks:
   - The Alertmanager receiver files, unless the bundle carries them.
     Production lists them in `faas_cp_state_extra_files` since 2026-11-20,
     but a bundle from a control plane that never ran a release with that
     list lacks them. Step 9's converge requires them. If the old host still
     runs, copy them from it without printing them:

     ```bash
     ssh <old host> 'sudo tar -C /etc/alertmanager -cf - secrets' \
       | ssh <admin>@<new host> 'sudo tar -C /etc/alertmanager --no-same-owner -xf - &&
           sudo chown -R alertmanager:alertmanager /etc/alertmanager/secrets &&
           sudo chmod 0400 /etc/alertmanager/secrets/*'
     ```

     Otherwise recreate them (`docs/runbooks/FaasWatchdog.md`).
   - Any other `faas_cp_state_extra_files` path the old host lacked.
9. Point the API's public name at the new host, then deploy with
   `cd-controlplane` (`CP_HOST` set to the new host). Deploy the release the
   old control plane ran, or one with no newer migrations: the database is
   already at its migrations.

   CD sees that the host has no `/opt/faas/current`. It converges every
   role and installs the release. Ordinarily it requires
   `https://api.gregale.dev/readyz` to be healthy before the switch. Here
   it instead requires that endpoint to answer ready within 5 minutes after
   the switch, which is why the name must point at the new host first. A
   unit release is refused until this full release is active.

   Then repoint the compute nodes and the public origin
   (`docs/runbooks/ControlPlaneFailover.md`, steps 8 and 9).
10. Run `make ANSIBLE_INVENTORY=… object-storage-reconcile` and the
   revocation review in `docs/runbooks/ObjectStorageRestoreReconcile.md`,
   then accept the restored database's timeline and turn `s3_enabled` back
   on. The restored bucket catalog is as old as the backup. Buckets created
   or deleted since then disagree with the providers, and revocations made
   since then are undone.

   ```bash
   sudo gregalectl object-storage accept --db-env /etc/faas/sealed.env
   ```
