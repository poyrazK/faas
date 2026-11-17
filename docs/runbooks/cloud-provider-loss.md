# Cloud provider loss

Use this when the provider that hosts the fleet stops serving us: a zone or
region outage, a suspended or locked project or billing account, or the
loss of the only account that can administer it. Today that provider is
Google Cloud: project `gregale-prod`, with the control plane `gregale-cp` and
`gregale-compute-1` in `us-east1-b` and `gregale-compute-2` in `us-east1-c`.
The project named in [gcp-public-beta-hardening.md](gcp-public-beta-hardening.md)
and `deploy/gcp/public-beta-policy.json` is the earlier beta project, which
has billing disabled. That audit does not cover production until it is
pointed at `gregale-prod`.

As of 2026-11-07, production has **no control-plane standby**: PostgreSQL on
`gregale-cp` has no streaming replica, and the control-plane state backup
timer is disabled. Losing `gregale-cp` means a rebuild (§4) from the
PostgreSQL basebackups and WAL, with the CA, keys and secrets re-created or
recovered from wherever else they are held.

The loss of one host is not this runbook. Use
[ControlPlaneFailover.md](ControlPlaneFailover.md) for the control plane and
[FaasComputeNodeDegraded.md](FaasComputeNodeDegraded.md) and
[twonode-fault-injection.md](twonode-fault-injection.md) for compute.

This is always SEV1. Open the incident per
[incident-response.md](incident-response.md) and page the platform owner
(Tier 2 in [escalation.md](../ops/escalation.md)).

## 1. Classify (first 10 minutes)

| Signal | Likely case | Go to |
|---|---|---|
| Provider status page shows an incident in our zone or region; console still works | Zone or region outage | §2 |
| Console or `gcloud` says the project is suspended, billing disabled, or access denied for the owner | Account action | §3 |
| Nobody who can administer the project can sign in | Lost administrator | §3 |
| Hosts are up but unreachable only through Cloudflare | Not this runbook | [third-party-outage.md](third-party-outage.md) |

Check from a workstation, not from the fleet:

```sh
gcloud projects describe <project-id> --format='value(lifecycleState)'
gcloud billing projects describe <project-id>
gcloud compute instances list --project <project-id> \
  --format='table(name,zone,status)'
```

Post a status update as soon as the impact is known
(`gregalectl status incident create`, see
[StatusPageDegraded.md](StatusPageDegraded.md)). If the status page itself is
down, use the fallback channels in escalation.md.

## 2. Zone or region outage

Warm apps keep serving from compute nodes that are still up: the gateway
holds its route table and the edge keeps its records (ADR-1344 static
stability). Deploys, wakes of parked apps and the API wait for the control
plane.

1. **Which side is down?** If only compute nodes are affected, drain them
   (`gregalectl compute-nodes drain --node <fqdn> --reason '<incident>'`)
   so placement and edge DNS move to the survivors
   ([FaasEdgeDNS.md](FaasEdgeDNS.md)).
2. **Control plane down, standby up** (standby in another zone): run the
   unplanned failover in [ControlPlaneFailover.md](ControlPlaneFailover.md).
   Production has no standby today, so this step does not apply there.
3. **Control plane and standby both down**: wait for the provider if its
   estimate is shorter than a rebuild (budget 2–4 hours for §4). Otherwise
   rebuild elsewhere, §4.
4. Freeze automatic deploys so a merge does not land on a half-fleet
   ([DeployFreeze.md](DeployFreeze.md)).

When the region returns, do not let the old primary rejoin as a primary.
Treat it as a fresh standby, as the failover runbook describes.

## 3. Suspended project or lost administrator

Everything in the project is at risk at once: instances, disks, snapshots
and any backup bucket in the same project or billing account.

1. Open a provider support case at the highest severity and record the
   case ID. For billing suspension, settle or dispute through the billing
   account; for a policy suspension, the appeal is in the console
   notification.
2. Do not delete, recreate or move resources while the case is open.
   Changes during a review can count against the appeal.
3. Decide on a deadline with the platform owner: if service is not back by
   then, rebuild elsewhere (§4). Write the deadline into the incident record.
4. If an administrator account is lost (not suspended), use the account
   recovery path for that identity, then add a second owner as soon as
   access returns (see "Before you need it").

## 4. Rebuild on another provider

This is the control-plane rebuild in
[ControlPlaneStateBackup.md](ControlPlaneStateBackup.md) ("Rebuild a control
plane from backups") plus new compute nodes. It needs the off-host backups
to be reachable **outside** the lost provider.

1. **Find the backups.** Read where `offhostbox` points
   (`/etc/faas/secrets/storage-box/rclone.conf` is sealed on the hosts; the
   operator copy lives with the backup credentials). If it is a bucket in
   the lost project, the backups are gone with it. Stop and escalate: the
   only remaining state is whatever is on the standby and any offline copy.
2. **Control plane.** Provision a host with the new provider, render the
   manifest for it, and follow ControlPlaneStateBackup.md steps 1–6:
   restore the sealed state bundle with the offline operator identity,
   bootstrap, restore PostgreSQL from the basebackup plus WAL
   ([PostgresBackup.md](PostgresBackup.md)), hold and reconcile object
   storage ([ObjectStorageRestoreReconcile.md](ObjectStorageRestoreReconcile.md)).
3. **Compute.** Join new nodes with
   [provider-neutral-node-join.md](provider-neutral-node-join.md). Snapshots
   on lost nodes are gone; apps cold-boot from their images on first wake.
4. **Edge.** Point the public records at the new hosts in Cloudflare
   ([EdgeDirectEntry.md](EdgeDirectEntry.md) for the entry records). DNS and
   Cloudflare do not depend on the lost provider, so this works while it is
   down.
5. **Object storage.** Buckets backed by the lost provider are unavailable
   until it returns. Say so in the status update; do not promise data you
   cannot reach.
6. Run the release acceptance (`deploy/scripts/production-release-acceptance.sh`)
   before saying the platform is back.

## Recovery and close

- Record RTO (detection to API serving) and RPO (newest restored WAL
  timestamp versus the last write before the loss).
- Re-run the provider audit (`scripts/ops/gcp_public_beta_audit.py` or its
  equivalent for the new provider).
- Post-mortem is required.

## Before you need it

Each of these turns a multi-day outage into hours. Check them at every
quarterly drill and record the result in [docs/drills/README.md](../drills/README.md).

- [ ] `offhostbox` points at storage in a **different provider and billing
      account** from the fleet, or a second copy exists there.
- [ ] The project has at least two owner identities, held by different
      people, both with hardware-key two-factor.
- [ ] The control-plane state operator identity (`cp-state-operator.key`)
      has an offline copy that does not live in the provider.
- [ ] Cloudflare, the registrar and GitHub accounts do not use an identity
      that lives in the lost provider (for example a Google Workspace login
      tied to the suspended billing account).
- [ ] The standby runs in a different zone from the primary.
- [ ] A rebuild on a second provider has been rehearsed end to end at least
      once (target: under 4 hours).
