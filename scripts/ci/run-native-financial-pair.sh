#!/usr/bin/env bash
# Dedicated controller entry point. Host-owned configuration keeps the SQL
# DSN and SSH identity out of CI arguments and uploaded evidence.
set -Eeuo pipefail
[[ "${EUID}" -eq 0 ]] || { echo 'financial pair: requires root' >&2; exit 1; }
config=/etc/faas/financial-pair.env
[[ "$(stat -c '%a %U' "${config}")" == '600 root' ]] || { echo 'financial pair: configuration must be root-owned 0600' >&2; exit 1; }
set -a
# shellcheck disable=SC1090
source "${config}"
set +a
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
exec python3 "${repo_root}/scripts/ci/financial-pair-runner.py"
