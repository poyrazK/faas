#!/usr/bin/env bash
# check_trimpath_safe.sh — static gate: first-party Go must not find its own
# source files with runtime.Caller(0).
#
# Local builds use -trimpath (the Makefile's GO_DEV_GOFLAGS) so that every
# checkout on a machine shares one copy of each package in the Go build cache.
# Under -trimpath, runtime.Caller names a file by its import path
# (github.com/onebox-faas/faas/pkg/x/y.go), not its absolute path, so
# filepath.Dir of it is not a directory. CI builds without -trimpath, so a
# test that does this passes in CI and fails on a developer machine.
#
# Instead:
#   - in a test, use a path relative to the package directory: go test runs
#     each test binary there (os.ReadFile("../../migrations/x.sql"));
#   - in a helper other packages' tests call, use e2etest.CallerDir().
#
# runtime.Caller(1) and deeper, which name a caller in a message, are fine.
set -euo pipefail

root="${1:-.}"

hits="$(cd "$root" && git ls-files -- '*.go' ':!:vendor/**' |
  xargs grep -Hn 'runtime\.Caller(0)' || true)"
if [[ -n "$hits" ]]; then
  echo "trimpath-check: runtime.Caller(0) locates source files, which breaks under -trimpath:" >&2
  echo "$hits" >&2
  echo "Use a path relative to the package directory in tests, or e2etest.CallerDir() (scripts/ci/check_trimpath_safe.sh)." >&2
  exit 1
fi
