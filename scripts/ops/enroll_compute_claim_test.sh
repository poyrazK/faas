#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT
mkdir -p "$test_root/bin"
printf '%s\n' 'api_version: gregale.dev/v1alpha1' >"$test_root/claim.yaml"

cat >"$test_root/bin/gregalectl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'gregalectl %s\n' "$*" >>"$FAKE_LOG"
if [[ "$*" == *"deploy claim validate"* ]]; then
  printf '%s\n' '{"valid":true,"node":"fsn-5"}'
elif [[ "$*" == *"deploy fleet-bundle create"* ]]; then
  while (($#)); do
    if [[ "$1" == --output ]]; then
      printf 'test fleet bundle\n' >"$2"
      exit 0
    fi
    shift
  done
  exit 2
fi
EOF

cat >"$test_root/bin/gcloud" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'gcloud %s\n' "$*" >>"$FAKE_LOG"
case "$*" in
  "auth list --filter=status:ACTIVE --format=value(account)") printf '%s\n' operator@example.com ;;
  "config get-value project") printf '%s\n' test-project ;;
esac
EOF

cat >"$test_root/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'gh %s\n' "$*" >>"$FAKE_LOG"
case "$1 $2" in
  "release download")
    while (($#)); do
      if [[ "$1" == --dir ]]; then
        printf 'fleet:\n  dynamic_compute:\n    enabled: true\n' >"$2/production-manifest.yaml"
        exit 0
      fi
      shift
    done
    ;;
  "run list")
    if [[ "$*" == *fleet-enrollment.yml* ]]; then
      printf '%s\n' 101
    else
      printf '%s\n' 202
    fi
    ;;
esac
EOF
chmod +x "$test_root/bin/gregalectl" "$test_root/bin/gcloud" "$test_root/bin/gh"

export PATH="$test_root/bin:$PATH"
export FAKE_LOG="$test_root/commands.log"
export GCP_PROJECT_ID=test-project
export GCP_OPERATOR_ACCOUNT=operator@example.com
export GCP_FLEET_ENROLLMENT_BUCKET=test-fleet-bucket
export GITHUB_REPOSITORY=example/faas
export GREGALECTL_BIN=gregalectl

bash "$repo_root/scripts/ops/enroll_compute_claim.sh" \
  --claim "$test_root/claim.yaml" --release-tag v0.1.18-rc.200 \
  >"$test_root/dry-run.out"
grep -Eq 'node=fsn-5 release=v0.1.18-rc.200 generation=[0-9]{18,20} digest=sha256:' "$test_root/dry-run.out"
grep -Fq 'dry run complete' "$test_root/dry-run.out"
if grep -Fq 'gcloud storage cp' "$FAKE_LOG"; then
  echo "dry-run unexpectedly uploaded the bundle" >&2
  exit 1
fi

: >"$FAKE_LOG"
bash "$repo_root/scripts/ops/enroll_compute_claim.sh" \
  --claim "$test_root/claim.yaml" --release-tag v0.1.18-rc.200 \
  --generation 1800000001 --no-wait --apply >"$test_root/apply.out"
grep -Fq 'gcloud storage cp ' "$FAKE_LOG"
grep -Fq -- '--if-generation-match=0' "$FAKE_LOG"
grep -Fq 'gh workflow run fleet-enrollment.yml' "$FAKE_LOG"
grep -Fq 'gh run watch 101' "$FAKE_LOG"
grep -Fq 'gh workflow run cd-compute.yml' "$FAKE_LOG"
if grep -Fq 'gh run watch 202' "$FAKE_LOG"; then
  echo "--no-wait unexpectedly watched the compute rollout" >&2
  exit 1
fi
grep -Fq 'compute_rollout=https://github.com/example/faas/actions/runs/202' "$test_root/apply.out"
grep -Fq 'bundle_signed_seconds=' "$test_root/apply.out"
grep -Fq 'enrollment_dispatched_seconds=' "$test_root/apply.out"
if grep -Fq 'enrollment_ready_seconds=' "$test_root/apply.out"; then
  echo "--no-wait unexpectedly reported the node ready" >&2
  exit 1
fi

echo "enroll_compute_claim tests passed"
