#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
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
  *"compute instances describe"*"--format=json"*)
    printf '%s\n' '{"networkInterfaces":[{"networkIP":"10.0.0.4"}]}'
    ;;
  *"compute instances describe"*)
    exit 1
    ;;
  *"ssh_host_ed25519_key.pub"*)
    printf '%s\n' 'SHA256:test-host-fingerprint'
    ;;
esac
EOF
chmod +x "$test_root/bin/gcloud"

export PATH="$test_root/bin:$PATH"
export FAKE_GCLOUD_LOG="$test_root/gcloud.log"
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
grep -Fq "useradd --create-home --user-group --shell /bin/bash 'faas-operator'" "$FAKE_GCLOUD_LOG"
grep -Fq "/etc/sudoers.d/90-gregale-operator" "$FAKE_GCLOUD_LOG"

if bash "$repo_root/scripts/ops/gcp_provision_compute.sh" \
    --instance faas-compute-node-5 --node fsn-5 --apply >"$test_root/missing.out" 2>&1; then
  echo "expected apply without operator identity to fail" >&2
  exit 1
fi
grep -Fq -- "--ssh-user is required with --apply" "$test_root/missing.out"

echo "gcp_provision_compute tests passed"
