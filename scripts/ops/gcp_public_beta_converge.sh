#!/usr/bin/env bash
# Idempotent GCP production controls. Dry-run is the default; --apply is an
# explicit maintenance action. The policy audit is the source of truth.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
project="${GCP_PROJECT_ID:-project-5ae37259-04cf-4070-bef}"
operator="${GCP_OPERATOR_ACCOUNT:-hpk.working@gmail.com}"
region="${GCP_REGION:-europe-west3}"
control="${GCP_CONTROL_INSTANCE:-faas-control-plane}"
backup_bucket="${GCP_BACKUP_BUCKET:-gregale-pg-backups-5ae37259}"
artifact_bucket="${GCP_ARTIFACT_BUCKET:-gregale-artifacts-5ae37259}"
backup_sa="gregale-backup@${project}.iam.gserviceaccount.com"
compute_sa="gregale-compute@${project}.iam.gserviceaccount.com"
control_sa="gregale-control@${project}.iam.gserviceaccount.com"
restore_sa="gregale-backup-restore@${project}.iam.gserviceaccount.com"
backup_role="projects/${project}/roles/gregaleBackupWriter"
alert_email="${GCP_ALERT_EMAIL:-$operator}"
cloudflare_v4_file="$root/deploy/ansible/roles/nftables/files/cloudflare-ips-v4.txt"
cloudflare_v6_file="$root/deploy/ansible/roles/nftables/files/cloudflare-ips-v6.txt"
phase=guard
apply=0

usage() {
  cat <<'USAGE'
Usage: gcp_public_beta_converge.sh [--phase guard|access|identity|budget|all] [--apply]

The default is a command-only dry run of the non-disruptive guard phase.
  guard     deletion protection, retained state disk, snapshots, log IAM/alert,
            and Data Access audit logs
  access    IAP + OS Login cutover and removal of public SSH/RDP/8080 rules
  identity  rolling dedicated VM identities and append-only backup access
  budget    budget API and a project-scoped alert budget
  all       every phase, in the order above

Live access and identity cutovers require their verification environment flags;
see docs/runbooks/gcp-public-beta-hardening.md.
USAGE
}

while (($#)); do
  case "$1" in
    --phase) phase="${2:?--phase requires a value}"; shift 2 ;;
    --apply) apply=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done
case "$phase" in guard|access|identity|budget|all) ;; *) echo "invalid phase: $phase" >&2; exit 2 ;; esac

active="$(gcloud auth list --filter=status:ACTIVE --format='value(account)' | paste -sd, -)"
[[ "$active" == "$operator" ]] || {
  echo "refusing: active gcloud account is '${active:-none}', expected '$operator'" >&2
  exit 1
}
configured_project="$(gcloud config get-value project 2>/dev/null)"
[[ "$configured_project" == "$project" ]] || {
  echo "refusing: active project is '$configured_project', expected '$project'" >&2
  exit 1
}

run() {
  printf '+'
  printf ' %q' "$@"
  printf '\n'
  ((apply == 0)) || "$@"
}

exists() { "$@" >/dev/null 2>&1; }
cloudflare_ranges() {
  local file="$1"
  [[ -s "$file" ]] || { echo "Cloudflare range file is missing or empty: $file" >&2; return 1; }
  paste -sd, "$file"
}

ensure_origin_firewall_rule() {
  local name="$1" ranges="$2"
  if exists gcloud compute firewall-rules describe "$name" --project="$project"; then
    run gcloud compute firewall-rules update "$name" --project="$project" \
      --source-ranges="$ranges" --target-tags=gregale-origin \
      --allow=tcp:80,tcp:443 --priority=800 --quiet
  else
    run gcloud compute firewall-rules create "$name" --project="$project" --network=default \
      --direction=INGRESS --priority=800 --action=ALLOW --rules=tcp:80,tcp:443 \
      --source-ranges="$ranges" --target-tags=gregale-origin --quiet
  fi
}
zone_of() {
  gcloud compute instances list --project="$project" --filter="name=($1)" \
    --format='value(zone.basename())' | head -1
}
disk_of() {
  gcloud compute instances describe "$1" --project="$project" --zone="$2" \
    --format='value(disks[0].source.basename())'
}
fleet_instances() {
  gcloud compute instances list --project="$project" --format='value(name,zone.basename())' \
    | awk -v control="$control" '$1 == control || $1 ~ /^faas-compute-node-/ { print }'
}

enable_api() {
  local api="$1"
  if ! gcloud services list --enabled --project="$project" --filter="name:$api" \
      --format='value(name)' | grep -Fq "/$api"; then
    run gcloud services enable "$api" --project="$project" --quiet
  fi
}

ensure_service_account() {
  local id="$1" display="$2"
  exists gcloud iam service-accounts describe "${id}@${project}.iam.gserviceaccount.com" --project="$project" \
    || run gcloud iam service-accounts create "$id" --project="$project" --display-name="$display" --quiet
}

ensure_project_role() {
  local member="$1" role="$2"
  if ! gcloud projects get-iam-policy "$project" \
      --flatten='bindings[].members' \
      --filter="bindings.role=$role AND bindings.members=$member" \
      --format='value(bindings.role)' | grep -Fqx "$role"; then
    run gcloud projects add-iam-policy-binding "$project" --member="$member" --role="$role" --quiet
  fi
}

service_account_has_role() {
  local resource="$1" member="$2" role="$3"
  gcloud iam service-accounts get-iam-policy "$resource" --project="$project" \
    --flatten='bindings[].members' \
    --filter="bindings.role=$role AND bindings.members=$member" \
    --format='value(bindings.role)' | grep -Fqx "$role"
}

ensure_service_account_role() {
  local resource="$1" member="$2" role="$3"
  service_account_has_role "$resource" "$member" "$role" \
    || run gcloud iam service-accounts add-iam-policy-binding "$resource" --project="$project" \
      --member="$member" --role="$role" --quiet
}

remove_service_account_role() {
  local resource="$1" member="$2" role="$3"
  service_account_has_role "$resource" "$member" "$role" \
    && run gcloud iam service-accounts remove-iam-policy-binding "$resource" --project="$project" \
      --member="$member" --role="$role" --quiet
  return 0
}

bucket_has_role() {
  local bucket="$1" member="$2" role="$3"
  gcloud storage buckets get-iam-policy "gs://$bucket" --format=json \
    | python3 -c 'import json,sys
member, role = sys.argv[1:]
policy = json.load(sys.stdin)
raise SystemExit(0 if any(row.get("role") == role and member in row.get("members", []) for row in policy.get("bindings", [])) else 1)' \
      "$member" "$role"
}

ensure_bucket_role() {
  local bucket="$1" member="$2" role="$3"
  if ((apply == 0)) && ! exists gcloud storage buckets describe "gs://$bucket"; then
    run gcloud storage buckets add-iam-policy-binding "gs://$bucket" \
      --member="$member" --role="$role" --quiet
    return
  fi
  bucket_has_role "$bucket" "$member" "$role" \
    || run gcloud storage buckets add-iam-policy-binding "gs://$bucket" \
      --member="$member" --role="$role" --quiet
}

remove_bucket_role() {
  local bucket="$1" member="$2" role="$3"
  bucket_has_role "$bucket" "$member" "$role" \
    && run gcloud storage buckets remove-iam-policy-binding "gs://$bucket" \
      --member="$member" --role="$role" --quiet
  return 0
}

create_monitoring_policy() {
  local policy_file="$1" channel="$2" attempt
  if ((apply == 0)); then
    run gcloud monitoring policies create --project="$project" \
      --policy-from-file="$policy_file" --notification-channels="$channel"
    return
  fi
  # Metric-based policies can take several minutes to become queryable after
  # Logging accepts a new descriptor. Direct log-match policies converge on
  # the first attempt; keeping the bounded retry here also covers the older
  # metric-based backup and Ops Agent policies.
  for attempt in $(seq 1 30); do
    if run gcloud monitoring policies create --project="$project" \
        --policy-from-file="$policy_file" --notification-channels="$channel"; then
      return 0
    fi
    ((attempt == 30)) || sleep 10
  done
  return 1
}

guard_phase() {
  enable_api compute.googleapis.com
  enable_api logging.googleapis.com
  enable_api monitoring.googleapis.com

  local name zone boot boot_auto_delete
  while read -r name zone; do
    [[ -n "$name" && -n "$zone" ]] || continue
    run gcloud compute instances update "$name" --project="$project" --zone="$zone" --deletion-protection --quiet
  done < <(fleet_instances)

  zone="$(zone_of "$control")"
  [[ -n "$zone" ]] || { echo "control instance is missing: $control" >&2; return 1; }
  boot="$(disk_of "$control" "$zone")"
  boot_auto_delete="$(gcloud compute instances describe "$control" --project="$project" --zone="$zone" \
    --format='value(disks[0].autoDelete)')"
  if [[ "$boot_auto_delete" != "False" ]]; then
    run gcloud compute instances set-disk-auto-delete "$control" --project="$project" --zone="$zone" \
      --disk="$boot" --no-auto-delete --quiet
  fi
  if ! exists gcloud compute resource-policies describe gregale-control-daily --project="$project" --region="$region"; then
    run gcloud compute resource-policies create snapshot-schedule gregale-control-daily \
      --project="$project" --region="$region" --daily-schedule --start-time=01:00 \
      --max-retention-days=14 --on-source-disk-delete=keep-auto-snapshots \
      --snapshot-labels=service=gregale,role=control-plane --quiet
  fi
  if ! gcloud compute disks describe "$boot" --project="$project" --zone="$zone" \
      --format='value(resourcePolicies.basename())' | grep -Fqx gregale-control-daily; then
    run gcloud compute disks add-resource-policies "$boot" --project="$project" --zone="$zone" \
      --resource-policies=gregale-control-daily --quiet
  fi

  # Keep current compute logging intact before the rolling identity cutover.
  while read -r account; do
    [[ -n "$account" ]] || continue
    ensure_project_role "serviceAccount:$account" roles/logging.logWriter
  done < <(gcloud compute instances list --project="$project" --filter='name~^faas-compute-node-' \
    --flatten=serviceAccounts --format='value(serviceAccounts.email)' | sort -u)

  local channel
  channel="$(gcloud beta monitoring channels list --project="$project" \
    --filter='displayName="Gregale operator email"' --format='value(name)' 2>/dev/null | head -1)"
  if [[ -z "$channel" ]]; then
    run gcloud beta monitoring channels create --project="$project" \
      --display-name='Gregale operator email' \
      --description='Primary notification path for Gregale public beta infrastructure alerts' \
      --type=email --channel-labels="email_address=$alert_email" --quiet
    if ((apply)); then
      # Monitoring notification channels are eventually consistent. A
      # successful create can take several seconds to appear in list output;
      # wait for it instead of failing an otherwise idempotent guard run.
      for _ in $(seq 1 12); do
        channel="$(gcloud beta monitoring channels list --project="$project" \
          --filter='displayName="Gregale operator email"' --format='value(name)' 2>/dev/null | head -1)"
        [[ -n "$channel" ]] && break
        sleep 5
      done
      [[ -n "$channel" ]] || { echo "created notification channel is not visible" >&2; return 1; }
    else
      channel="projects/$project/notificationChannels/created-during-apply"
    fi
  fi

  if ! exists gcloud logging metrics describe gregale_ops_agent_export_failures --project="$project"; then
    run gcloud logging metrics create gregale_ops_agent_export_failures --project="$project" \
      --config-from-file="$root/deploy/gcp/ops-agent-export-metric.yaml"
  fi
  if ! gcloud monitoring policies list --project="$project" --format='value(displayName)' \
      | grep -Fqx 'Gregale Ops Agent export failures'; then
    create_monitoring_policy "$root/deploy/gcp/ops-agent-export-alert.json" "$channel"
  fi

  local metric alert
  for metric in backup-object-delete backup-bulk-read infrastructure-iam-change origin-firewall-change; do
    if ! exists gcloud logging metrics describe "gregale_${metric//-/_}" --project="$project"; then
      run gcloud logging metrics create "gregale_${metric//-/_}" --project="$project" \
        --config-from-file="$root/deploy/gcp/${metric}-metric.yaml"
    fi
  done
  while IFS='|' read -r alert metric; do
    if ! gcloud monitoring policies list --project="$project" --format='value(displayName)' \
        | grep -Fqx "$alert"; then
      create_monitoring_policy "$root/deploy/gcp/${metric}-alert.json" "$channel"
    fi
  done <<'ALERTS'
Gregale backup object deletion|backup-object-delete
Gregale backup bulk reads|backup-bulk-read
Gregale infrastructure IAM changes|infrastructure-iam-change
Gregale origin firewall changes|origin-firewall-change
ALERTS

  local retention
  retention="$(gcloud logging buckets describe _Default --location=global --project="$project" \
    --format='value(retentionDays)')"
  if ((retention < 30)); then
    run gcloud logging buckets update _Default --location=global --project="$project" \
      --retention-days=30 --quiet
  fi

  local old_policy new_policy
  old_policy="$(mktemp)"
  new_policy="$(mktemp)"
  trap 'rm -f "$old_policy" "$new_policy"' RETURN
  gcloud projects get-iam-policy "$project" --format=json >"$old_policy"
  python3 "$root/scripts/ops/gcp_public_beta_iam.py" add-audit-logs "$old_policy" "$new_policy"
  if ! cmp -s "$old_policy" "$new_policy"; then
    run gcloud projects set-iam-policy "$project" "$new_policy" --quiet
  fi
  rm -f "$old_policy" "$new_policy"
  trap - RETURN
}

access_phase() {
  [[ "$apply" == 0 || ("${GCLOUD_IAP_SSH_VERIFIED:-}" == 1 && "${GCLOUD_IAP_CD_VERIFIED:-}" == 1) ]] || {
    echo "refusing access cutover: set GCLOUD_IAP_SSH_VERIFIED=1 and GCLOUD_IAP_CD_VERIFIED=1 after both runbook tests" >&2
    return 1
  }
  ensure_project_role "user:$operator" roles/compute.osAdminLogin
  ensure_project_role "user:$operator" roles/iap.tunnelResourceAccessor

  local cloudflare_v4 cloudflare_v6 control_zone
  cloudflare_v4="$(cloudflare_ranges "$cloudflare_v4_file")"
  cloudflare_v6="$(cloudflare_ranges "$cloudflare_v6_file")"
  ensure_origin_firewall_rule gregale-cloudflare-origin-v4 "$cloudflare_v4"
  ensure_origin_firewall_rule gregale-cloudflare-origin-v6 "$cloudflare_v6"

  control_zone="$(zone_of "$control")"
  [[ -n "$control_zone" ]] || { echo "control instance is missing: $control" >&2; return 1; }
  run gcloud compute instances add-tags "$control" --project="$project" --zone="$control_zone" \
    --tags=gregale-origin --quiet
  if ! exists gcloud compute firewall-rules describe gregale-iap-ssh --project="$project"; then
    run gcloud compute firewall-rules create gregale-iap-ssh --project="$project" --network=default \
      --direction=INGRESS --priority=900 --action=ALLOW --rules=tcp:22 \
      --source-ranges=35.235.240.0/20 --target-tags=gregale-admin --quiet
  fi
  local name zone
  while read -r name zone; do
    [[ -n "$name" && -n "$zone" ]] || continue
    run gcloud compute instances add-tags "$name" --project="$project" --zone="$zone" --tags=gregale-admin --quiet
    run gcloud compute instances add-metadata "$name" --project="$project" --zone="$zone" \
      --metadata=enable-oslogin=TRUE,block-project-ssh-keys=TRUE --quiet
  done < <(fleet_instances)
  for rule in default-allow-ssh default-allow-rdp allow-faas-8080 allow-http-https default-allow-http default-allow-https; do
    if exists gcloud compute firewall-rules describe "$rule" --project="$project"; then
      run gcloud compute firewall-rules delete "$rule" --project="$project" --quiet
    fi
  done
}

identity_phase() {
  [[ "$apply" == 0 || ("${GCLOUD_IDENTITY_CUTOVER_VERIFIED:-}" == 1 && "${GCLOUD_BACKUP_IMPERSONATION_VERIFIED:-}" == 1) ]] || {
    echo "refusing identity cutover: set GCLOUD_IDENTITY_CUTOVER_VERIFIED=1 and GCLOUD_BACKUP_IMPERSONATION_VERIFIED=1 after the runbook checks" >&2
    return 1
  }
  enable_api iamcredentials.googleapis.com
  ensure_service_account gregale-compute 'Gregale compute node'
  ensure_service_account gregale-control 'Gregale control plane'
  ensure_service_account gregale-backup 'Gregale append-only backup writer'
  ensure_service_account gregale-backup-restore 'Gregale backup restore'
  ensure_project_role "serviceAccount:$compute_sa" roles/logging.logWriter
  ensure_project_role "serviceAccount:$compute_sa" roles/monitoring.metricWriter
  ensure_project_role "serviceAccount:$control_sa" roles/logging.logWriter
  ensure_project_role "serviceAccount:$control_sa" roles/monitoring.metricWriter
  if ! exists gcloud storage buckets describe "gs://$artifact_bucket"; then
    run gcloud storage buckets create "gs://$artifact_bucket" --project="$project" \
      --location="$region" --default-storage-class=STANDARD \
      --uniform-bucket-level-access --public-access-prevention --quiet
  else
    run gcloud storage buckets update "gs://$artifact_bucket" \
      --uniform-bucket-level-access --public-access-prevention --quiet
  fi

  if ! exists gcloud iam roles describe gregaleBackupWriter --project="$project"; then
    run gcloud iam roles create gregaleBackupWriter --project="$project" \
      --file="$root/deploy/gcp/backup-writer-role.yaml" --quiet
  else
    run gcloud iam roles update gregaleBackupWriter --project="$project" \
      --file="$root/deploy/gcp/backup-writer-role.yaml" --quiet
  fi
  ensure_bucket_role "$backup_bucket" "serviceAccount:$backup_sa" "$backup_role"
  ensure_bucket_role "$backup_bucket" "serviceAccount:$restore_sa" roles/storage.objectViewer
  ensure_bucket_role "$artifact_bucket" "serviceAccount:$compute_sa" roles/storage.objectUser
  ensure_bucket_role "$artifact_bucket" "serviceAccount:$control_sa" roles/storage.objectUser
  ensure_service_account_role "$backup_sa" "serviceAccount:$control_sa" roles/iam.serviceAccountTokenCreator

  local name zone desired current original_status
  while read -r name zone; do
    [[ -n "$name" && -n "$zone" ]] || continue
    desired="$compute_sa"
    [[ "$name" != "$control" ]] || desired="$control_sa"
	current="$(gcloud compute instances describe "$name" --project="$project" --zone="$zone" \
	  --format='value(serviceAccounts[0].email)')"
	if [[ "$current" == "$desired" ]]; then
	  printf '# %s already uses %s; skipping identity restart\n' "$name" "$desired"
	  continue
	fi
    original_status="$(gcloud compute instances describe "$name" --project="$project" --zone="$zone" --format='value(status)')"
    if [[ "$original_status" == RUNNING ]]; then
      run gcloud compute instances stop "$name" --project="$project" --zone="$zone" --quiet
    fi
    run gcloud compute instances set-service-account "$name" --project="$project" --zone="$zone" \
      --service-account="$desired" --scopes=cloud-platform --quiet
    if [[ "$original_status" == RUNNING ]]; then
      run gcloud compute instances start "$name" --project="$project" --zone="$zone" --quiet
    fi
  done < <(fleet_instances)

  # Remove destructive storage access only after the release-current control
  # identity is attached.  The backup helper then mints one-hour tokens for
  # the append-only writer; compute identities never receive bucket access.
  local default_compute_sa='811654175645-compute@developer.gserviceaccount.com'
  for account in "$default_compute_sa" "$compute_sa"; do
    remove_bucket_role "$backup_bucket" "serviceAccount:$account" roles/storage.objectAdmin
  done
  remove_bucket_role "$backup_bucket" "serviceAccount:$backup_sa" roles/storage.objectAdmin
  remove_service_account_role "$backup_sa" "serviceAccount:$default_compute_sa" roles/iam.serviceAccountTokenCreator
}

budget_phase() {
  enable_api billingbudgets.googleapis.com
  local account
  account="$(gcloud billing projects describe "$project" --format='value(billingAccountName.basename())')"
  [[ -n "$account" ]] || { echo "billing account is not visible" >&2; return 1; }
  if gcloud beta billing budgets list --billing-account="$account" \
      --filter='displayName="Gregale public beta"' --format='value(displayName)' \
      | grep -Fqx 'Gregale public beta'; then
    echo "+ budget 'Gregale public beta' already exists"
    return
  fi
  [[ -n "${GCP_BETA_BUDGET_AMOUNT:-}" ]] || {
    echo "GCP_BETA_BUDGET_AMOUNT is required to create the missing budget (for example 100USD)" >&2
    return 1
  }
  run gcloud beta billing budgets create --billing-account="$account" \
    --display-name='Gregale public beta' --budget-amount="$GCP_BETA_BUDGET_AMOUNT" \
    --calendar-period=month --filter-projects="projects/$project" \
    --threshold-rule=percent=0.5 --threshold-rule=percent=0.8 \
    --threshold-rule=percent=1.0 --threshold-rule=percent=1.0,basis=forecasted-spend
}

case "$phase" in
  guard) guard_phase ;;
  access) access_phase ;;
  identity) identity_phase ;;
  budget) budget_phase ;;
  all) guard_phase; access_phase; identity_phase; budget_phase ;;
esac

if ((apply)); then
  python3 "$root/scripts/ops/gcp_public_beta_audit.py"
else
  echo "dry run complete; re-run with --apply during the documented maintenance step"
fi
