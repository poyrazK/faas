#!/usr/bin/env bash
# Offline checks by default. --remote also exercises a prepared Gregale account.
set -euo pipefail
if [[ $# -gt 1 || ( $# -eq 1 && $1 != --remote ) ]]; then
  echo 'Usage: bash check.sh [--remote]' >&2
  exit 2
fi
cd "$(dirname "${BASH_SOURCE[0]}")"
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
cli=${GREGALE_CLI:-gregale}
plan=${REPORT_PLAN:-pro}
environment=${REPORT_ENVIRONMENT_ID:-11111111-1111-4111-8111-111111111111}
if [[ ${1:-} == --remote ]]; then
  : "${REPORT_ENVIRONMENT_ID:?set the ready PDF dependency environment UUID}"
  : "${REPORT_TEST_KEY:?set a stable idempotency key for this CI attempt}"
fi
python3 make_task.py "$environment" "$work/task.json"
"$cli" run tasks validate --manifest "$work/task.json" --plan "$plan" --input @input.json
"$cli" run tasks generate --manifest "$work/task.json" --plan "$plan" --language typescript --check bindings.ts
"$cli" run tasks generate --manifest "$work/task.json" --plan "$plan" --language python --check bindings.py
if [[ ${1:-} == --remote ]]; then
  python3 make_sample.py "$work/input.pdf"
  "$cli" run tasks test --manifest "$work/task.json" --input @input.json \
    --input-file "input.pdf=$work/input.pdf" --output-dir "$work/artifacts" \
    --idempotency-key "$REPORT_TEST_KEY" --json > "$work/receipt.json"
  python3 - "$work" <<'PY'
import json
import sys
from pathlib import Path

root = Path(sys.argv[1])
receipt = json.loads((root / "receipt.json").read_text())
expected = {"pages": 1, "words": 5, "preview_truncated": False}
if receipt["result"] != expected:
    raise SystemExit("The sample PDF report returned unexpected counts")
if "Customer report from Gregale Run." not in (root / "artifacts" / "report.html").read_text():
    raise SystemExit("The sample PDF report is missing the extracted text")
print(f"PDF smoke test passed (Run {receipt['id']}).")
PY
fi
