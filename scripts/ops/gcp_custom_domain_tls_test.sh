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
# Without GCP_NETWORK the rules keep joining the 'default' VPC.
grep -F -- 'firewall-rules create gregale-custom-domains-v4' "$work/plan" | grep -Fq -- '--network=default'

# Prod 2026-11-16: gregale-prod's VPC is 'gregale', but every rule was created
# on --network=default, where it never admits the fleet's traffic.
GCP_PROJECT_ID=project-5ae37259-04cf-4070-bef GCP_OPERATOR_ACCOUNT=hpk.working@gmail.com \
  GCP_CONTROL_INSTANCE=faas-control-plane GCP_NETWORK=gregale PATH="$work:$PATH" \
  bash "$root/scripts/ops/gcp_public_beta_converge.sh" --phase access > "$work/plan-network"
for rule in gregale-custom-domains-v4 gregale-custom-domains-v6 gregale-cloudflare-origin-v4 gregale-iap-ssh; do
  grep -F -- "firewall-rules create $rule " "$work/plan-network" | grep -Fq -- '--network=gregale' || {
    echo "$rule was not created on GCP_NETWORK" >&2
    exit 1
  }
done
if grep -Fq -- '--network=default' "$work/plan-network"; then
  echo 'a rule still names the default network with GCP_NETWORK set' >&2
  exit 1
fi
echo 'custom-domain GCP access plan: OK'
