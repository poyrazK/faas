# GCP public-beta hardening and cost guard

Gregale's GCP beta fleet is owned by `hpk.working@gmail.com` and billed through
the linked billing account. The repository policy records the operator identity
and project ID separately: changing the active `gcloud` account never changes
project ownership or billing linkage.

The executable policy covers issues #2346 and #2351–#2356 plus #2360. It checks
the live project, not a cached inventory:

- Cloud Ops Agent write IAM and an alert for dropped exports;
- deletion protection, retained control-plane state disk, and daily snapshots;
- IAP/OS Login access with project keys blocked and no public SSH, RDP, or 8080;
- separate control/compute identities and a backup writer without delete/IAM;
- Storage and IAM Data Access logs;
- SSD-backed active compute capacity and paid disks on stopped nodes;
- billing linkage, budget visibility, thresholds, and a notification target.

## Audit

Use the owner account requested for normal platform operations. The audit is
strict: unavailable APIs and insufficient IAM are findings because silence
would make the green result misleading.

```sh
gcloud config set account hpk.working@gmail.com
gcloud config set project project-5ae37259-04cf-4070-bef
python3 scripts/ops/gcp_public_beta_audit.py
```

For an incident attachment, save the raw provider response locally. It can be
replayed without network access and must not be committed because instance
metadata can contain SSH keys.

```sh
python3 scripts/ops/gcp_public_beta_audit.py \
  --write-snapshot /tmp/gregale-gcp-audit.json --json
python3 scripts/ops/gcp_public_beta_audit.py \
  --snapshot /tmp/gregale-gcp-audit.json
```

## Converge

The convergence tool defaults to a dry run. Review that output in the change
record, then apply one phase at a time. `guard` is online-safe and should run
first.

```sh
bash scripts/ops/gcp_public_beta_converge.sh --phase guard
bash scripts/ops/gcp_public_beta_converge.sh --phase guard --apply
```

The guard phase enables the logging/monitoring APIs, protects all fleet VMs,
retains the control disk, attaches a 14-day daily snapshot schedule, restores
log-writer IAM for the currently attached compute identity, and enables Data
Access audit logs for Cloud Storage and IAM with at least 30 days of retention.
It creates alerts for Ops Agent export failures, destructive backup operations,
backup read bursts, and project or backup-bucket IAM changes. Confirm a fresh
compute log and a harmless backup-object read appear in Cloud Logging after it
runs.

The beta threat model keeps Cloud Audit Logs in the production project. Every
sink or retention-policy mutation creates an Admin Activity event, and the IAM
change alert pages the operator. Before a separate security account exists, an
off-project sink would share the same human administrator without creating an
independent trust boundary. Revisit that decision when a separate security
account is available.

### Administrative access cutover

The access phase is separate because deleting the old public SSH rule before
IAP works would lock out the only control plane. First grant and exercise OS
Login through IAP from a second terminal:

```sh
gcloud compute ssh faas-control-plane --zone=europe-west3-a \
  --tunnel-through-iap --command='id && sudo -n true'
```

Keep that session open, render the access plan, and apply it only after the
test succeeds. The apply guard requires an explicit record of that test.

```sh
bash scripts/ops/gcp_public_beta_converge.sh --phase access
GCLOUD_IAP_SSH_VERIFIED=1 \
  bash scripts/ops/gcp_public_beta_converge.sh --phase access --apply
```

This creates an IAP-only SSH rule, grants the named operator OS Admin Login and
IAP tunnel access, enables OS Login, blocks project SSH keys, and deletes the
three known public administrative rules. The host-hardening Ansible role keeps
password authentication and direct root login off. It refuses to apply that
posture while Ansible is itself connected as root. Rerun bootstrap through the
named OS Login administrator and prove IAP sudo once more before closing the
retained session.

### Identity and backup cutover

The identity phase stops instances while changing their attached service
account. Keep one release-current compute node admitted while changing the
other, drain it, and verify a snapshot restore on the changed node before
moving on. The control-plane change requires a maintenance window.

The custom backup writer role contains only object create/get/list. It supports
`rclone copy` plus byte verification while preventing deletion and IAM changes.
`gregale-backup-restore` receives read-only object access. Retention remains a
bucket lifecycle responsibility.

```sh
bash scripts/ops/gcp_public_beta_converge.sh --phase identity
GCLOUD_IDENTITY_CUTOVER_VERIFIED=1 \
  bash scripts/ops/gcp_public_beta_converge.sh --phase identity --apply

systemctl start faas-pg-basebackup.service
systemctl start faas-pg-basebackup-push.service
deploy/scripts/pg-restore-verify.sh
```

Do not change both compute identities in one outage window. The script prints
the exact rolling operations in dry-run mode; execute the phase per node if the
fleet cannot preserve capacity for its generated sequence.

### Budget

The billing account owner must grant the operating identity Billing Account
Costs Manager (`roles/billing.costsManager`) or an equivalent custom role on
the billing account. Project Owner alone cannot list or create budgets.

Set the monthly amount in the billing account currency. The tool creates 50%,
80%, 100%, and forecasted-100% thresholds scoped to this project and retains
the billing-account IAM recipients.

```sh
GCP_BETA_BUDGET_AMOUNT=100USD \
  bash scripts/ops/gcp_public_beta_converge.sh --phase budget --apply
```

The remaining credit is an account-level promotion and is not a safe budget
amount. Choose the amount from the expected monthly baseline, then review Cost
Table by SKU weekly for Compute Engine, persistent disk, Logging ingestion,
network egress, and backup storage.

## Availability and stopped capacity

The beta target is one active SSD compute node plus a release-current recovery
path. Application autoscaling only adds Firecracker guests within that host;
it does not replace a lost GCE VM. Keep a stopped node for at most 24 hours
while it has paid disks. Beyond that window, either start and roll it as the
declared recovery node or snapshot required evidence and delete the VM/disks.

The audit fails when active compute has no SSD, when no compute node runs, or
when a stopped node retains disks beyond 24 hours. For public beta, record these
timings during every node replacement drill:

1. failure detection and scheduler drain;
2. GCE VM and SSD provisioning;
3. signed release installation and node admission;
4. snapshot readiness and first successful restore;
5. customer traffic recovery.

The current beta recovery objective is 20 minutes from confirmed host loss to
an admitted replacement. If repeated drills cannot meet that objective, keep a
second release-current SSD node running; a stopped legacy HDD node is not a
standby.

The provider step is scripted and timed. It creates a private, deletion-
protected N2 host with nested virtualization and a retained 100 GB `pd-ssd`,
then emits a host-key-pinned `ComputeNodeClaim` for the existing signed
enrollment path:

```sh
bash scripts/ops/gcp_provision_compute.sh \
  --instance faas-compute-node-3 --node fsn-3
bash scripts/ops/gcp_provision_compute.sh \
  --instance faas-compute-node-3 --node fsn-3 --apply
```

The final line records `provider_ready_seconds`. Record the later release,
admission, snapshot, and traffic timestamps beside it; VM creation alone is not
traffic recovery.
