#!/usr/bin/env bash

# Exit successfully when a newline-delimited changed-path list can affect the
# full per-migration Postgres test package. The caller deliberately runs the
# suite unconditionally on main and manual dispatches; this classifier keeps
# unrelated pull requests from replaying the full migration history hundreds
# of times.
set -euo pipefail

while IFS= read -r path; do
  path="${path#./}"
  case "$path" in
    migrations/*|pkg/api/*|pkg/cursor/*|pkg/db/*|pkg/dispatch/*|pkg/hostport/*|pkg/productcap/*|pkg/publicstatus/*|pkg/reqbudget/*|pkg/sourcecontext/*|pkg/state/*|pkg/statefuldenylist/*|go.mod|go.sum)
      exit 0
      ;;
  esac
done

exit 1
