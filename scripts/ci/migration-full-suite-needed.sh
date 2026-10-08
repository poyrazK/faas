#!/usr/bin/env bash

# Exit successfully when a newline-delimited changed-path list can affect the
# full per-migration Postgres test package. The caller deliberately runs the
# suite unconditionally on main and manual dispatches; this classifier keeps
# unrelated pull requests from replaying the full migration history hundreds
# of times.
set -euo pipefail

# Drain the producer even after a match. An early exit makes a large diff's
# writer receive SIGPIPE; under pipefail the workflow then misclassifies that
# relevant diff as unrelated and skips the full suite.
result=1

while IFS= read -r path; do
  path="${path#./}"
  case "$path" in
    .github/workflows/ci.yml|scripts/ci/e2eshard/*|scripts/ci/migration-full-suite-needed*|migrations/*|pkg/api/*|pkg/cursor/*|pkg/db/*|pkg/dispatch/*|pkg/hostport/*|pkg/productcap/*|pkg/publicstatus/*|pkg/reqbudget/*|pkg/sourcecontext/*|pkg/state/*|pkg/statefuldenylist/*|go.mod|go.sum)
      result=0
      ;;
  esac
done

exit "$result"
