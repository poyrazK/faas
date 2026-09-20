#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
xfs_tasks="$repo_root/deploy/ansible/roles/xfs/tasks/main.yml"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT
mkdir -p "$test_root/bin"

cat >"$test_root/operator.pub" <<'EOF'
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ3tNvVyUmeZZMn5ZiA3FwPm1lr0YH3/AHqMFKuCbpT3 gregale-compute-deploy-test
EOF

cat >"$test_root/bin/gcloud" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_GCLOUD_LOG"
case "$*" in
  "auth list --filter=status:ACTIVE --format=value(account)")
    printf '%s\n' 'operator@example.com'
    ;;
  "config get-value project")
    printf '%s\n' 'test-project'
    ;;
  *"compute instances create"*)
    touch "$FAKE_INSTANCE_STATE"
    ;;
  *"compute instances describe"*)
    [[ -f "$FAKE_INSTANCE_STATE" ]] || exit 1
    cat <<JSON
{"name":"faas-compute-node-4","zone":"zones/test-zone","machineType":"machineTypes/${FAKE_MACHINE_TYPE:-n2-standard-4}","status":"RUNNING","deletionProtection":true,"advancedMachineFeatures":{"enableNestedVirtualization":true},"networkInterfaces":[{"network":"networks/default","networkIP":"10.0.0.4"}],"serviceAccounts":[{"email":"gregale-compute@test-project.iam.gserviceaccount.com","scopes":["https://www.googleapis.com/auth/cloud-platform"]}],"metadata":{"items":[{"key":"enable-oslogin","value":"TRUE"},{"key":"block-project-ssh-keys","value":"TRUE"}]},"tags":{"items":["gregale-admin"]},"labels":{"service":"gregale","role":"compute","recovery":"managed"},"disks":[{"boot":true,"autoDelete":false,"diskSizeGb":"100"},{"boot":false,"deviceName":"faas-fc-storage","autoDelete":false,"diskSizeGb":"100"}]}
JSON
    ;;
  *"dns managed-zones describe"*)
    [[ "${FAKE_DNS_ZONE_EXISTS:-0}" == 1 ]] || exit 1
    printf '%s\n' '{"dnsName":"fsn-4.gregale.dev.","visibility":"private","privateVisibilityConfig":{"networks":[{"networkUrl":"networks/default"}]}}'
    ;;
  *"dns record-sets describe"*)
    [[ "${FAKE_DNS_RECORD_EXISTS:-0}" == 1 ]] || exit 1
    printf '{"rrdatas":["%s"]}\n' "${FAKE_DNS_RECORD_IP:-10.0.0.4}"
    ;;
  *"ssh_host_ed25519_key.pub"*)
    printf '%s\n' 'SHA256:test-host-fingerprint'
    ;;
esac
EOF
chmod +x "$test_root/bin/gcloud"

export PATH="$test_root/bin:$PATH"
export FAKE_GCLOUD_LOG="$test_root/gcloud.log"
export FAKE_INSTANCE_STATE="$test_root/instance.exists"
export GCP_PROJECT_ID=test-project
export GCP_OPERATOR_ACCOUNT=operator@example.com

claim="$test_root/fsn-4.yaml"
bash "$repo_root/scripts/ops/gcp_provision_compute.sh" \
  --instance faas-compute-node-4 \
  --node fsn-4 \
  --zone test-zone \
  --claim "$claim" \
  --ssh-user faas-operator \
  --ssh-public-key-file "$test_root/operator.pub" \
  --apply >/dev/null

grep -Fq "user: faas-operator" "$claim"
grep -Fq "host: 10.0.0.4" "$claim"
grep -Fq "host_key_sha256: SHA256:test-host-fingerprint" "$claim"
grep -Fq -- "--boot-disk-type=pd-standard" "$FAKE_GCLOUD_LOG"
grep -Fq -- "type=pd-ssd" "$FAKE_GCLOUD_LOG"
grep -Fq -- "--network=default" "$FAKE_GCLOUD_LOG"
grep -Fq "dns managed-zones create gregale-fsn-4-private" "$FAKE_GCLOUD_LOG"
grep -Fq "dns record-sets create fsn-4.gregale.dev." "$FAKE_GCLOUD_LOG"
grep -Fq "useradd --create-home --user-group --shell /bin/bash 'faas-operator'" "$FAKE_GCLOUD_LOG"
grep -Fq 'test -n "$operator_home"' "$FAKE_GCLOUD_LOG"
grep -Fq '"$operator_home/.ssh/authorized_keys"' "$FAKE_GCLOUD_LOG"
grep -Fq "/etc/sudoers.d/90-gregale-operator" "$FAKE_GCLOUD_LOG"
# The GCP join converges the fast-storage cache bind mount before the legacy
# XFS role validates the same device. Only inspect the device's root mount so
# that the role remains idempotent once /var/lib/faas/cache is bound.
grep -Fq 'argv: [findmnt, -S, "{{ faas_storage_device }}", -n, -o, TARGET, --first-only]' "$xfs_tasks"

# An interrupted run resumes the exact managed VM, reuses an already-correct
# private DNS identity, and never attempts a second instance creation.
: >"$FAKE_GCLOUD_LOG"
export FAKE_DNS_ZONE_EXISTS=1
export FAKE_DNS_RECORD_EXISTS=1
bash "$repo_root/scripts/ops/gcp_provision_compute.sh" \
  --instance faas-compute-node-4 \
  --node fsn-4 \
  --zone test-zone \
  --claim "$claim" \
  --ssh-user faas-operator \
  --ssh-public-key-file "$test_root/operator.pub" \
  --resume-existing \
  --apply >/dev/null
if grep -Fq "compute instances create" "$FAKE_GCLOUD_LOG"; then
  echo "resume path attempted to create a duplicate instance" >&2
  exit 1
fi
if grep -Fq "dns record-sets create" "$FAKE_GCLOUD_LOG" || grep -Fq "dns record-sets update" "$FAKE_GCLOUD_LOG"; then
  echo "resume path rewrote an already-correct private DNS record" >&2
  exit 1
fi

# A stale private A record is repaired to the validated instance IP.
: >"$FAKE_GCLOUD_LOG"
export FAKE_DNS_RECORD_IP=10.0.0.99
bash "$repo_root/scripts/ops/gcp_provision_compute.sh" \
  --instance faas-compute-node-4 \
  --node fsn-4 \
  --zone test-zone \
  --claim "$claim" \
  --ssh-user faas-operator \
  --ssh-public-key-file "$test_root/operator.pub" \
  --resume-existing \
  --apply >/dev/null
grep -Fq "dns record-sets update fsn-4.gregale.dev." "$FAKE_GCLOUD_LOG"
grep -Fq -- "--rrdatas=10.0.0.4" "$FAKE_GCLOUD_LOG"
unset FAKE_DNS_RECORD_IP

# Resume is fail-closed when a same-named machine does not match the managed
# compute contract.
export FAKE_MACHINE_TYPE=e2-standard-4
if bash "$repo_root/scripts/ops/gcp_provision_compute.sh" \
    --instance faas-compute-node-4 --node fsn-4 --zone test-zone \
    --resume-existing >"$test_root/mismatch.out" 2>&1; then
  echo "expected mismatched existing instance to fail validation" >&2
  exit 1
fi
grep -Fq "machine type must be n2-standard-4" "$test_root/mismatch.out"
unset FAKE_MACHINE_TYPE

if bash "$repo_root/scripts/ops/gcp_provision_compute.sh" \
    --instance faas-compute-node-5 --node fsn-5 --apply >"$test_root/missing.out" 2>&1; then
  echo "expected apply without operator identity to fail" >&2
  exit 1
fi
grep -Fq -- "--ssh-user is required with --apply" "$test_root/missing.out"

echo "gcp_provision_compute tests passed"
