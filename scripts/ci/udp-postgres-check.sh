#!/usr/bin/env bash
# Require actual PostgreSQL execution; a skipped integration test is not acceptance.
set -euo pipefail
: "${DATABASE_URL:?DATABASE_URL must point to an isolated test PostgreSQL cluster}"
if [[ -n "${FAAS_SKIP_PG_TESTS:-}" ]]; then
  echo 'udp-postgres-check: FAAS_SKIP_PG_TESTS must be unset' >&2
  exit 1
fi
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"
python3 - "$repo_root" <<'PY'
import json
import pathlib
import re
import subprocess
import sys

root = pathlib.Path(sys.argv[1])
expected = set()
for package, filename in [
    ('github.com/onebox-faas/faas/cmd/gatewayd-public', 'cmd/gatewayd-public/udp_ingress_pg_test.go'),
    ('github.com/onebox-faas/faas/pkg/state', 'pkg/state/pgstore_udp_listeners_test.go'),
    ('github.com/onebox-faas/faas/pkg/state', 'pkg/state/pgstore_udp_recovery_test.go'),
    ('github.com/onebox-faas/faas/pkg/state', 'pkg/state/pgstore_listener_app_purge_test.go'),
    ('github.com/onebox-faas/faas/migrations', 'migrations/20260930193000001_app_udp_listeners_test.go'),
]:
    names = re.findall(r'^func (Test\w+)\(t \*testing.T\)', (root / filename).read_text(), re.M)
    if not names:
        raise SystemExit(f'udp-postgres-check: no acceptance tests in {filename}')
    expected.update((package, name) for name in names)
pattern = '^(' + '|'.join(sorted({name for _, name in expected})) + ')$'
passed = set()
failed = False
process = subprocess.Popen(['go', 'test', '-json', '-p', '1', '-count=1', '-run', pattern,
    './pkg/state', './migrations', './cmd/gatewayd-public'], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
for line in process.stdout:
    try:
        event = json.loads(line)
    except json.JSONDecodeError:
        print(line, end='')
        continue
    identity = (event.get('Package'), event.get('Test'))
    if event.get('Action') == 'pass' and identity in expected:
        passed.add(identity)
        print(f'PASS {identity[1]}')
    if event.get('Action') in ('skip', 'fail'):
        failed = True
        print(f'udp-postgres-check: {event["Action"]}: {identity}', file=sys.stderr)
    output = event.get('Output', '')
    if '--- FAIL:' in output or 'Error' in output or 'error:' in output:
        print(output, end='')
code = process.wait()
missing = expected - passed
if code or failed or missing:
    for _, name in sorted(missing):
        print(f'udp-postgres-check: required pass missing: {name}', file=sys.stderr)
    raise SystemExit(1)
print(f'udp-postgres-check: all {len(expected)} required PostgreSQL tests passed')
PY
