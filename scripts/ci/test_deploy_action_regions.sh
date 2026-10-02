#!/usr/bin/env bash
# Exercise the Action runner; --cli also tests the actual CLI/API wire contract.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
stage="$(mktemp -d)"
server_pid=""
cleanup() {
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$stage"
}
trap cleanup EXIT
mkdir -p "$stage/action/bin" "$stage/action/src" "$stage/home"
echo test-version > "$stage/action/src/version.txt"
cat > "$stage/action/bin/gregale" <<'CLI'
#!/usr/bin/env bash
set -euo pipefail
if [ "${2:-}" = --help ]; then
  if [ "${MOCK_SUPPORT:-true}" = true ]; then
    echo '  --region REGION  immutable compute region'
  fi
  exit 0
fi
printf '%s\n' "$@" >> "$ARGV_FILE"
echo '{"id":"00000000000000000000000000000001","status":"queued"}'
CLI
chmod +x "$stage/action/bin/gregale"
run_action() {
  env -i PATH="$PATH" HOME="$stage/home" ACTION_PATH="$stage/action" \
    FAAS_TOKEN=fixture-nonsecret FAAS_API=http://127.0.0.1:1 \
    INPUT_APP=action-ref-canary INPUT_REPO=acme/example INPUT_REF=main \
    INPUT_WAIT=false RUNNER_TEMP="$stage" GITHUB_OUTPUT="$stage/output" \
    ARGV_FILE="$stage/argv" "$@" bash "$root/.github/actions/deploy/src/run.sh" deploy
}
for region in eu-fsn a "$(printf '%063d' 0)"; do
  : > "$stage/argv"
  : > "$stage/output"
  run_action INPUT_REGION="$region" INPUT_ENVIRONMENT=staging > "$stage/log" 2>&1
  python3 - "$stage/argv" "$region" <<'PY'
import sys
from pathlib import Path
args = Path(sys.argv[1]).read_text().splitlines()
assert args[args.index('--region') + 1] == sys.argv[2], args
assert args.count('--region') == 1, args
assert args[args.index('--environment') + 1] == 'staging', args
PY
  grep -Fxq status=queued "$stage/output"
done
# Literal injection candidate must not expand.
# shellcheck disable=SC2016
for region in 'EU-fsn' ' eu-fsn' 'eu_fsn' '-eu' 'eu-' 'eu;echo injected' 'eu${{bad}}' "$(printf '%064d' 0)" $'eu\nfsn'; do
  : > "$stage/argv"
  if run_action INPUT_REGION="$region" > "$stage/log" 2>&1; then
    echo 'invalid region was accepted' >&2
    exit 1
  fi
  grep -Fq 'region must be a canonical' "$stage/log"
  test ! -s "$stage/argv"
done
: > "$stage/argv"
if run_action INPUT_REGION=eu-fsn MOCK_SUPPORT=false > "$stage/log" 2>&1; then
  echo 'older CLI accepted a regional deployment' >&2
  exit 1
fi
grep -Fq 'bundled CLI does not support --region' "$stage/log"
test ! -s "$stage/argv"
# Empty input preserves placement and works with an older CLI, including wait.
: > "$stage/argv"
: > "$stage/output"
run_action INPUT_REGION= MOCK_SUPPORT=false INPUT_WAIT=true > "$stage/log" 2>&1
if grep -Fxq -- --region "$stage/argv"; then
  echo 'omitted region was forwarded to CLI' >&2
  exit 1
fi
grep -Fxq status=live "$stage/output"
echo 'deploy Action regional input checks passed'

if [ "${1:-}" = --cli ] && [ "$#" = 2 ]; then
  cp "$2" "$stage/action/bin/gregale"
  python3 "$root/scripts/ci/github-action-fixture-server.py" \
    --port 0 --port-file "$stage/port" --compute-region eu-test \
    --request-log "$stage/requests" > "$stage/server.log" 2>&1 &
  server_pid=$!
  for _attempt in 1 2 3 4 5; do
    if [ -s "$stage/port" ] && curl --fail --silent "http://127.0.0.1:$(cat "$stage/port")/healthz" >/dev/null; then
      break
    fi
    sleep 1
  done
  test -s "$stage/port"
  : > "$stage/output"
  run_action INPUT_REGION=eu-test FAAS_API="http://127.0.0.1:$(cat "$stage/port")" > "$stage/log" 2>&1 || {
    cat "$stage/log" >&2
    cat "$stage/gregale-deploy-action.stderr" >&2
    exit 1
  }
  grep -Fxq status=queued "$stage/output"
  grep -Fxq 'POST /v1/apps' "$stage/requests"
  grep -Fxq 'POST /v1/apps/action-ref-canary/deployments/source-ref' "$stage/requests"
  echo 'deploy Action real CLI regional wire canary passed'
elif [ "$#" != 0 ]; then
  echo 'usage: test_deploy_action_regions.sh [--cli /path/to/gregale]' >&2
  exit 64
fi
