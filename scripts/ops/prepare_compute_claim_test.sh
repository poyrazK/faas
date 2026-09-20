#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT
mkdir -p "$test_root/bin"
touch "$test_root/identity"
chmod 0600 "$test_root/identity"

cat >"$test_root/bin/ssh-keyscan" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
echo '203.0.113.27 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestOnlyKeyMaterial'
EOF

cat >"$test_root/bin/ssh-keygen" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
echo "256 ${FAKE_SCANNED_FINGERPRINT:?} host (ED25519)"
EOF

cat >"$test_root/bin/ssh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
printf '%s\n' "$*" >"${FAKE_SSH_ARGS:?}"
if [[ "${FAKE_SSH_FAIL:-0}" == 1 ]]; then
  echo 'remote preflight failed' >&2
  exit 1
fi
echo 'host_preflight=ready architecture=x86_64 kvm=ready storage=/dev/disk/by-id/provider-data'
EOF
chmod +x "$test_root/bin/ssh-keyscan" "$test_root/bin/ssh-keygen" "$test_root/bin/ssh"

fingerprint='SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
export FAKE_SCANNED_FINGERPRINT="$fingerprint"
export FAKE_SSH_ARGS="$test_root/ssh.args"
claim="$test_root/fsn-9.yaml"

PATH="$test_root/bin:$PATH" bash "$repo_root/scripts/ops/prepare_compute_claim.sh" \
  --node fsn-9 --ssh-host 203.0.113.27 --ssh-user faas-operator --ssh-port 2222 \
  --identity-file "$test_root/identity" --host-key-sha256 "$fingerprint" \
  --storage-device /dev/disk/by-id/provider-data --format-storage --claim "$claim"

grep -q '^  name: fsn-9$' "$claim"
grep -q '^    host: 203.0.113.27$' "$claim"
grep -q '^    user: faas-operator$' "$claim"
grep -q '^    port: 2222$' "$claim"
grep -q "^    host_key_sha256: $fingerprint$" "$claim"
grep -q '^    device: /dev/disk/by-id/provider-data$' "$claim"
grep -q '^    format: true$' "$claim"
grep -q -- '-p 2222' "$test_root/ssh.args"
grep -q -- 'StrictHostKeyChecking=yes' "$test_root/ssh.args"
grep -q -- '/dev/disk/by-id/provider-data 1' "$test_root/ssh.args"

export FAKE_SCANNED_FINGERPRINT='SHA256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB'
if PATH="$test_root/bin:$PATH" bash "$repo_root/scripts/ops/prepare_compute_claim.sh" \
    --node fsn-10 --ssh-host 203.0.113.28 --identity-file "$test_root/identity" \
    --host-key-sha256 "$fingerprint" --storage-device /dev/disk/by-id/provider-data \
    --claim "$test_root/mismatch.yaml" >"$test_root/mismatch.out" 2>&1; then
  echo 'mismatched SSH host key unexpectedly succeeded' >&2
  exit 1
fi
grep -q 'SSH host key mismatch' "$test_root/mismatch.out"
[[ ! -e "$test_root/mismatch.yaml" ]]

export FAKE_SCANNED_FINGERPRINT="$fingerprint"
export FAKE_SSH_FAIL=1
if PATH="$test_root/bin:$PATH" bash "$repo_root/scripts/ops/prepare_compute_claim.sh" \
    --node fsn-10 --ssh-host 203.0.113.28 --identity-file "$test_root/identity" \
    --host-key-sha256 "$fingerprint" --storage-device /dev/disk/by-id/provider-data \
    --claim "$test_root/remote-failure.yaml" >"$test_root/remote-failure.out" 2>&1; then
  echo 'failed remote preflight unexpectedly emitted a claim' >&2
  exit 1
fi
grep -q 'remote preflight failed' "$test_root/remote-failure.out"
[[ ! -e "$test_root/remote-failure.yaml" ]]
unset FAKE_SSH_FAIL

if PATH="$test_root/bin:$PATH" bash "$repo_root/scripts/ops/prepare_compute_claim.sh" \
    --node fsn-10 --ssh-host 203.0.113.28 --identity-file "$test_root/identity" \
    --host-key-sha256 "$fingerprint" --storage-device /dev/sdb \
    --claim "$test_root/unstable.yaml" >"$test_root/unstable.out" 2>&1; then
  echo 'unstable storage path unexpectedly succeeded' >&2
  exit 1
fi
grep -q 'stable /dev/disk/by-id path' "$test_root/unstable.out"

echo 'prepare_compute_claim tests passed'
