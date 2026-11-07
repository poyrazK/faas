# Operator credential compromise

Use this when an operator's laptop, phone, password manager or a single
credential may be in someone else's hands: a lost or stolen device, malware
on a workstation, a phishing click that entered credentials, or a token
pasted somewhere public.

If the credential could reach GitHub release or deploy paths, also follow
[release-pipeline-compromise.md](release-pipeline-compromise.md). If it could
open sealed customer secrets, also follow
[customer-secret-store-compromise.md](customer-secret-store-compromise.md).

Severity: SEV1 if the operator had production access, SEV2 otherwise. Treat
"we don't know what was on the laptop" as production access.

## 1. Inventory what the operator could reach

Write this list into the incident record first, then work down it in the
order of section 2. Ask the operator; do not rely on memory alone.

| Credential | Where it lives | What it reaches |
|---|---|---|
| Google account / `gcloud` | browser session, `~/.config/gcloud` | GCP project: hosts via IAP and OS Login, disks, backups, IAM |
| GitHub session, PATs, `gh` token, SSH keys | browser, `~/.config/gh`, `~/.ssh` | source, workflows, environment secrets, releases |
| Cloudflare login and API tokens | browser, env files | DNS, proxy, WAF, Workers |
| Registrar account | browser | the domain delegation |
| Gregale CLI session and API keys | `~/.config/gregale`, env | operator and customer API |
| Age identities (`cp-state-operator.key`, seal-domain identities) | wherever they were created | customer secrets, control-plane state backups |
| Pushover device | phone | receives pages; an attacker can see alert contents |
| Polar, email provider, Grafana | browser | billing, mail, metrics |

## 2. Revoke (first 30 minutes)

Work from a different, trusted device.

1. **Google.** Change the password, sign out of all sessions from the
   account security page, and remove the device. Then check the project:

   ```sh
   gcloud projects get-iam-policy <project-id> --format=json
   gcloud compute os-login ssh-keys list --account <operator account>
   ```

   Remove any SSH key you cannot attribute, and any IAM binding added in the
   window. The `infrastructure-iam-change` alert in `deploy/gcp/` should
   have fired for IAM changes; check it.
2. **GitHub.** Revoke all sessions, PATs, SSH keys and OAuth grants for the
   account; review the security log for new keys, tokens and repository
   accesses.
3. **Cloudflare and the registrar.** Change passwords, revoke API tokens the
   operator created, and check each audit log for DNS or account changes.
   Any change there moves to
   [dns-registrar-compromise.md](dns-registrar-compromise.md).
4. **Gregale.** Revoke the operator's own sessions and keys:

   ```sh
   sudo gregalectl accounts revoke-sessions --account-id <operator account uuid> \
     --reason credential --yes
   ```

   If you cannot tell which accounts were used, revoke platform-wide with
   `gregalectl break-glass revoke-credentials --kinds sessions,api_keys`
   (dry run first; see customer-secret-store-compromise.md §1).
5. **Age identities.** Any identity file on the device is exposed. Rotate it
   (customer-secret-store-compromise.md §3). For `cp-state-operator.key`,
   generate a new one, re-seal the control-plane state backup to it, and
   delete old bundles once a new backup has been verified
   ([ControlPlaneStateBackup.md](ControlPlaneStateBackup.md)).
6. **Pushover.** Remove the device from the Pushover account so pages stop
   reaching it, and confirm the replacement receives a test page.

## 3. Look for use

For the window from the earliest possible exposure to now:

- GCP audit logs: admin activity and data access by the operator identity,
  especially `compute.instances.setMetadata`, IAM changes, and bucket reads
  of backups (the `backup-bulk-read` alert in `deploy/gcp/`).
- Host logins: `journalctl _COMM=sshd` and `last` on each host.
- Gregale audit: `gregalectl accounts activity --account-id <uuid> --limit 100`.
- GitHub security log and workflow runs started by the account.

If anything was used, the incident is now a breach: follow
[breach-notification.md](breach-notification.md) from §2.

## 4. Close

- Reissue credentials to the operator on a clean device.
- Record what was on the device and what was rotated.
- Post-mortem if anything was used, or if the inventory in §1 turned up a
  credential nobody knew about.

## Before you need it

- [ ] Every operator keeps the §1 inventory current in the private roster.
- [ ] Hardware security keys on Google, GitHub, Cloudflare and the registrar.
- [ ] Long-lived age identities are not kept on daily-use laptops.
- [ ] Full-disk encryption and remote wipe on every operator device.
