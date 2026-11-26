#!/bin/sh
set -eu
: "${DATABASE_URL:?Set DATABASE_URL to a PostgreSQL test cluster whose role can create and drop private databases}"
if [ -n "${FAAS_SKIP_PG_TESTS:-}" ]; then
  echo 'Commit load qualification cannot skip PostgreSQL tests.' >&2
  exit 1
fi
if ! command -v promtool >/dev/null 2>&1; then
  echo 'Commit load qualification requires promtool for alert rule simulation.' >&2
  exit 1
fi
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
promtool check rules deploy/ansible/roles/prometheus/files/faas.rules.yml
promtool test rules deploy/ansible/roles/prometheus/files/commit.rules.test.yml
results=$(mktemp "${TMPDIR:-/tmp}/gregale-commit-load.XXXXXX")
trap 'rm -f "$results"' EXIT HUP INT TERM
printf '%s\n' 'Running isolated Commit load and recovery qualification (default: 75 seconds plus setup and drain).'
if ! GREGALE_COMMIT_LOAD_TEST=1 "${GO:-go}" test -p 1 -json -timeout 20m -count=1 -run '^TestCommitSustainedLoadAndRecovery$' ./pkg/commitload > "$results"; then
  cat "$results"
  exit 1
fi
python3 - "$results" <<'PY'
import json
import sys
passed = False
reports = []
for line in open(sys.argv[1], encoding="utf-8"):
    event = json.loads(line)
    if event.get("Test") == "TestCommitSustainedLoadAndRecovery":
        if event.get("Action") in ("fail", "skip"):
            raise SystemExit("Commit load qualification failed or was skipped")
        if event.get("Action") == "pass":
            passed = True
    output = event.get("Output", "")
    if "COMMIT_LOAD_RESULT " in output:
        reports.append(json.loads(output.split("COMMIT_LOAD_RESULT ", 1)[1]))
if not passed or len(reports) != 1:
    raise SystemExit("Commit load qualification did not produce one passing result")
print(json.dumps(reports[0], indent=2, sort_keys=True))
PY
