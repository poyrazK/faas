#!/usr/bin/env bash
# Static + dry-run smoke test for issue #252's operator drill.

set -euo pipefail

SCRIPT="$(cd "$(dirname "$0")" && pwd)/faas-tls-cutover-drill.sh"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/faas-tls-cutover-test.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

bash -n "$SCRIPT"

for token in \
  "Five-step validation matrix" \
  "Dashboard banner persistence" \
  "TLS cutover/reload" \
  "TLS rollback" \
  "FAAS_TLS_DNS_TOOL"; do
  grep -Fq "$token" "$SCRIPT" || {
    echo "missing drill token: $token" >&2
    exit 1
  }
done

FAAS_DRILL_RECORD_DIR="$TMP/records" \
FAAS_DRILL_OPERATOR=tester \
FAAS_DRILL_BOX=test-box \
FAAS_TLS_PUBLIC_ENDPOINT=https://example.invalid \
bash "$SCRIPT" --dry-run >/dev/null

record="$(find "$TMP/records" -type f -name '*-tls-cutover-drill.md' -print -quit)"
[[ -n "$record" ]] || { echo "dry-run did not write a record" >&2; exit 1; }
grep -Fq '| Verdict | **PASS** |' "$record"
grep -Fq '| 5 | Post-rollback verification |' "$record"

echo "tls-cutover-drill: PASS"
