#!/usr/bin/env bash
set -euo pipefail

hotfix_test_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
exec python3 -m unittest discover -s "$hotfix_test_root/scripts/ops" -p 'request_evidence_hotfix_test.py' -v
