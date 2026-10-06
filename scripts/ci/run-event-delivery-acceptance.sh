#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != Linux ]]; then
  echo "Process delivery acceptance requires Linux capability introspection; run on Linux CI or a Linux host. KVM is not required." >&2
  exit 1
fi

if [[ -z "${DATABASE_URL:-}" || -n "${FAAS_SKIP_PG_TESTS:-}" ]]; then
  echo "Event delivery acceptance requires DATABASE_URL and refuses FAAS_SKIP_PG_TESTS." >&2
  exit 1
fi
if ! command -v psql >/dev/null || ! psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc 'select 1' >/dev/null; then
  echo "Event delivery acceptance requires reachable PostgreSQL; skipped tests cannot qualify the gate." >&2
  exit 1
fi

report="${1:-$(mktemp -t event-delivery-acceptance.XXXXXX)}"
status=0
"${GO:-go}" test -race -json -count=1 -timeout=15m \
  -run '^TestE2E_EventDeliveryRecovery_' ./cmd/e2e >"$report" || status=$?

if ! python3 - "$report" <<'PY'
import json
import sys

required = {
    "TestE2E_EventDeliveryRecovery_WholeReceipt",
    "TestE2E_EventDeliveryRecovery_IndependentRecipients",
}
terminal = {}
with open(sys.argv[1], encoding="utf-8") as handle:
    for line in handle:
        row = json.loads(line)
        if row.get("Test") in required and row.get("Action") in {"pass", "fail", "skip"}:
            terminal[row["Test"]] = row["Action"]
failures = {name: terminal.get(name, "missing") for name in sorted(required)
            if terminal.get(name) != "pass"}
print(json.dumps({"gate": "fail" if failures else "pass", "tests": terminal,
                  "failures": failures}, sort_keys=True))
sys.exit(bool(failures))
PY
then
  status=1
fi

if [[ "$status" != 0 ]]; then
  echo "Event delivery acceptance failed; full evidence: $report" >&2
elif [[ $# == 0 ]]; then
  rm -f "$report"
fi
exit "$status"
