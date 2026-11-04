#!/usr/bin/env bash
# Exercise the access plan without contacting GCP or changing infrastructure.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cat > "$work/gcloud" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  'auth list '*) printf '%s\n' hpk.working@gmail.com ;;
  'config get-value project') printf '%s\n' project-5ae37259-04cf-4070-bef ;;
  'projects get-iam-policy '*) printf '%s\n' roles/compute.osAdminLogin roles/iap.tunnelResourceAccessor ;;
  'compute firewall-rules describe '*) exit 1 ;;
  'compute instances list '*--filter=*) printf '%s\n' europe-west3-b ;;
  'compute instances list '*) printf '%s\n' 'faas-control-plane europe-west3-b' 'faas-compute-node-1 europe-west3-c' ;;
  *) printf 'unexpected gcloud invocation: %s\n' "$*" >&2; exit 1 ;;
esac
SH
chmod +x "$work/gcloud"
GCP_PROJECT_ID=project-5ae37259-04cf-4070-bef GCP_OPERATOR_ACCOUNT=hpk.working@gmail.com \
  GCP_CONTROL_INSTANCE=faas-control-plane PATH="$work:$PATH" \
  bash "$root/scripts/ops/gcp_public_beta_converge.sh" --phase access > "$work/plan"
for family in v4 v6; do
  grep -F -- "firewall-rules create gregale-custom-domains-$family" "$work/plan" | grep -Fq -- '--target-tags=gregale-custom-domain-edge'
done
grep -F -- 'instances add-tags faas-control-plane' "$work/plan" | grep -Fq -- '--tags=gregale-custom-domain-edge'
if grep -F -- 'instances add-tags faas-compute-node-1' "$work/plan" | grep -Fq -- '--tags=gregale-custom-domain-edge'; then
  echo 'custom-domain ingress was assigned to a compute node' >&2
  exit 1
fi
grep -Fq -- '--source-ranges=0.0.0.0/0 --target-tags=gregale-custom-domain-edge' "$work/plan"
grep -Fq -- '--source-ranges=::/0 --target-tags=gregale-custom-domain-edge' "$work/plan"
echo 'custom-domain GCP access plan: OK'
