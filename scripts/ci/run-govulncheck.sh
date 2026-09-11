#!/usr/bin/env bash
# Run govulncheck in JSON mode without confusing findings with tool failure.
# Exit 3 means reachable vulnerabilities were found, so its JSON must reach
# the normalizer and issue-creation path. Other non-zero exits fail closed.

set -euo pipefail

output=${1:?output JSON path is required}
shift
if [[ "$#" -eq 0 ]]; then
  echo "at least one package pattern is required" >&2
  exit 2
fi

govulncheck_bin=${GOVULNCHECK_BIN:-govulncheck}

set +e
"${govulncheck_bin}" -format=json "$@" > "${output}"
status=$?
set -e

case "${status}" in
  0|3)
    ;;
  *)
    echo "govulncheck failed with exit code ${status}" >&2
    exit "${status}"
    ;;
esac

if [[ ! -s "${output}" ]]; then
  echo "govulncheck produced an empty result" >&2
  exit 1
fi
