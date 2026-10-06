#!/bin/sh
set -eu
: "${DATABASE_URL:?Set DATABASE_URL to the disposable native acceptance PostgreSQL cluster}"
: "${FAAS_TEST_KERNEL:?Set FAAS_TEST_KERNEL to the qualified guest kernel}"
: "${FAAS_BUILDER_BASE_PATH:?Set FAAS_BUILDER_BASE_PATH to the qualified builder base}"
if [ "$(uname -s)" != Linux ] || [ "$(uname -m)" != x86_64 ] || [ ! -r /dev/kvm ] || [ "$(id -u)" != 0 ]; then
  echo 'Managed workflow native acceptance requires a native x86_64 Linux KVM host with root.' >&2
  exit 1
fi
if [ -n "${FAAS_SKIP_PG_TESTS:-}" ]; then
  echo 'Managed workflow native acceptance cannot disable PostgreSQL tests.' >&2
  exit 1
fi
if [ ! -r "$FAAS_TEST_KERNEL" ] || [ ! -r "$FAAS_BUILDER_BASE_PATH" ]; then
  echo 'FAAS_TEST_KERNEL and FAAS_BUILDER_BASE_PATH must be readable.' >&2
  exit 1
fi
for task_tool in firecracker jailer ip nft; do
  command -v "$task_tool" >/dev/null || { echo "Missing native dependency: $task_tool" >&2; exit 1; }
done
task_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$task_root"
task_results=$(mktemp "${TMPDIR:-/tmp}/gregale-managed-operation-native.XXXXXX")
trap 'rm -f "$task_results"' EXIT HUP INT TERM
if ! "${GO:-go}" test -p 1 -race -tags metal -json ./cmd/e2e -count=1 -timeout 30m \
  -run '^TestManagedOperationWorkflowMetal$' > "$task_results"; then
  cat "$task_results"
  exit 1
fi
python3 - "$task_results" <<'PY'
import json
import sys

required = "TestManagedOperationWorkflowMetal"
passed = False
skipped = False
for line in open(sys.argv[1]):
    event = json.loads(line)
    if event.get("Test") != required:
        continue
    passed |= event.get("Action") == "pass"
    skipped |= event.get("Action") == "skip"
if skipped or not passed:
    raise SystemExit("Native managed workflow acceptance did not execute and pass.")
print("Native managed workflow guest recovery and effect delivery gate passed.")
PY
