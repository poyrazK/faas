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

3. Converge the control plane. The role installs
   `/etc/faas/cp-state-backup.{paths,recipients}` and starts
   `faas-cp-state-backup.timer` (daily 04:15 UTC). To run it once now:
   `systemctl start faas-cp-state-backup.service`.

4. Check the result:

   ```bash
   journalctl -u faas-cp-state-backup.service --since -1h
   cat /var/lib/node_exporter/textfile_collector/faas_cp_state_backup.prom
   ```

To add paths, such as Alertmanager receiver files outside `/etc/faas`, set
`faas_cp_state_extra_files`. Both the backup and the standby take them.

## Alerts

| Alert | Meaning | Act |
|---|---|---|
| `FaasControlPlaneStateBackupFailed` | The unit failed. | Read the journal. Usual causes: the rclone remote is missing or unreachable (`/etc/faas/secrets/storage-box/rclone.conf`), the installed `gregalectl` predates `backup seal-cp-state`, or a state file over 16 MB. |
| `FaasControlPlaneStateBackupStale` | No verified push for 26 hours. | Same checks. Also check that the timer is enabled (`systemctl list-timers faas-cp-state-backup.timer`). |

## Rebuild a control plane from backups

Use this when both the control plane and the standby are lost. With a
healthy standby, fail over instead.

1. Provision the replacement host and render a manifest whose control-plane
   entry names it, keeping the old `address`
   (`docs/runbooks/ControlPlaneFailover.md`, step 4).
2. Copy the newest bundle from `offhostbox:faas-cp-state/<old host>/` to the
   new host. Then, with the operator identity, which you bring for this step
   only and then delete:

   ```bash
   gregalectl backup restore-cp-state --identity cp-state-operator.key \
     --in cp-state-<time>.tar.age --stage /var/lib/faas-standby
   shred -u cp-state-operator.key
   ```

3. Install the files **before** the first bootstrap, so bootstrap keeps the
   restored CA and HMAC keys instead of generating new ones:

   ```bash
   ANSIBLE_CONFIG=deploy/ansible/ansible.cfg ansible-playbook \
     -i deploy/ansible/.generated/inventory/hosts.ini deploy/ansible/cp_state_restore.yml
   ```

4. Give the new host its own `host.age` (`gregalectl secrets init`), then
   re-seal the values sealed outside PostgreSQL for it
   (`docs/runbooks/FaasFleetSealDomain.md`).
5. Run `make … bootstrap-control-plane`, restore PostgreSQL from the
   off-host basebackups (`docs/runbooks/PostgresBackup.md`), then deploy the
   release and repoint the compute nodes and public origin as in the failover
   runbook.
6. Run `make ANSIBLE_INVENTORY=… object-storage-reconcile` before turning
   `s3_enabled` back on. The restored bucket catalog is as old as the backup;
   buckets created or deleted since then disagree with the providers
   (`docs/runbooks/ObjectStorageRestoreReconcile.md`).
